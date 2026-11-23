package control

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	dialTimeout        = 5 * time.Second
	replyTimeout       = 5 * time.Second
	replicaCheckPeriod = time.Second
	defaultLease       = 15 * time.Second
	minBackoff         = time.Second
)

// WorkerSnapshot is what the worker reports at one moment. Source functions
// are called each time a message is built, so the leader never gets a stale list.
type WorkerSnapshot struct {
	Hardware   HardwareInfo
	Replicas   []ReplicaInfo
	Statuses   []ReplicaStatus
	CPUPercent float64
	RAMUsedMB  int64
}

// WorkerConfig holds the worker's identity and timing.
type WorkerConfig struct {
	NodeID            string
	Secret            []byte
	Address           string
	HTTPPort          int
	HeartbeatInterval time.Duration
	MaxBackoff        time.Duration
	Snapshot          func() WorkerSnapshot
	Now               func() time.Time
}

// Resolver finds the leader's control address as host:port. It runs in the
// DISCOVERING state. The control package does not depend on UDP discovery.
type Resolver func(ctx context.Context) (string, error)

// Worker runs the worker state machine of spec §4.3:
// DISCOVERING -> CONNECTING -> REGISTERING -> REGISTERED, and back to
// DISCOVERING on any error.
type Worker struct {
	cfg WorkerConfig
}

func NewWorker(cfg WorkerConfig) *Worker {
	return &Worker{cfg: cfg}
}

func (w *Worker) now() time.Time {
	if w.cfg.Now != nil {
		return w.cfg.Now()
	}
	return time.Now()
}

// Run runs until ctx is cancelled. Between failed attempts it waits with
// backoff and jitter. A session that reaches REGISTERED resets the backoff.
func (w *Worker) Run(ctx context.Context, resolve Resolver) {
	bo := NewBackoff(minBackoff, w.cfg.MaxBackoff)
	for ctx.Err() == nil {
		addr, err := resolve(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := bo.Next()
			log.Printf("worker: discovery failed (%v); retrying in %s", err, wait)
			sleepCtx(ctx, wait)
			continue
		}

		registered, err := w.session(ctx, addr)
		if ctx.Err() != nil {
			return
		}
		if registered {
			bo.Reset()
		}
		wait := bo.Next()
		log.Printf("worker: connection to %s ended (%v); returning to discovery in %s", addr, err, wait)
		sleepCtx(ctx, wait)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// session runs one connection to the leader. registered reports whether
// REGISTER_ACK was received, so the caller knows to reset backoff.
func (w *Worker) session(ctx context.Context, addr string) (registered bool, err error) {
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		r := bufio.NewReader(conn)
		for {
			line, err := ReadLine(r)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case lines <- line:
			case <-done:
				return
			}
		}
	}()

	snap := w.cfg.Snapshot()
	reg, err := NewEnvelope(TypeRegister, w.cfg.NodeID, w.now(), RegisterPayload{
		Address:  w.cfg.Address,
		HTTPPort: w.cfg.HTTPPort,
		Hardware: snap.Hardware,
		Replicas: snap.Replicas,
	})
	if err != nil {
		return false, err
	}
	if err := w.write(conn, reg); err != nil {
		return false, err
	}

	lease, err := w.awaitAck(ctx, lines, readErr)
	if err != nil {
		return false, err
	}

	lastAck := w.now()
	prev := make(map[string]string, len(snap.Replicas))
	for _, r := range snap.Replicas {
		prev[r.ID] = r.State
	}
	hb := time.NewTicker(w.cfg.HeartbeatInterval)
	defer hb.Stop()
	check := time.NewTicker(replicaCheckPeriod)
	defer check.Stop()

	for {
		select {
		case <-ctx.Done():
			w.deregister(conn)
			return true, ctx.Err()

		case err := <-readErr:
			return true, fmt.Errorf("connection lost: %w", err)

		case line := <-lines:
			env, err := w.verify(line)
			if err != nil {
				return true, err
			}
			switch env.Type {
			case TypeHeartbeatAck:
				lastAck = w.now()
			case TypeError:
				var ep ErrorPayload
				_ = DecodeStrict(env.Payload, &ep)
				return true, fmt.Errorf("leader error %s: %s", ep.Code, ep.Message)
			default:
				return true, fmt.Errorf("unexpected %s from leader", env.Type)
			}

		case <-hb.C:
			if w.now().Sub(lastAck) > lease {
				return true, fmt.Errorf("no heartbeat ack within %s", lease)
			}
			if err := w.sendHeartbeat(conn); err != nil {
				return true, err
			}

		case <-check.C:
			snap := w.cfg.Snapshot()
			for _, st := range snap.Statuses {
				if prev[st.ID] == st.State {
					continue
				}
				prev[st.ID] = st.State
				env, err := NewEnvelope(TypeReplicaUpdate, w.cfg.NodeID, w.now(), st)
				if err != nil {
					return true, err
				}
				if err := w.write(conn, env); err != nil {
					return true, err
				}
			}
		}
	}
}

// awaitAck waits for REGISTER_ACK and returns the lease in seconds.
func (w *Worker) awaitAck(ctx context.Context, lines <-chan []byte, readErr <-chan error) (time.Duration, error) {
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
		return 0, errors.New("no reply to REGISTER")
	case err := <-readErr:
		return 0, fmt.Errorf("connection lost during registration: %w", err)
	case line := <-lines:
		env, err := w.verify(line)
		if err != nil {
			return 0, err
		}
		switch env.Type {
		case TypeRegisterAck:
			var ack RegisterAckPayload
			if err := DecodeStrict(env.Payload, &ack); err != nil {
				return 0, err
			}
			if ack.LeaseSeconds <= 0 {
				return defaultLease, nil
			}
			return time.Duration(ack.LeaseSeconds) * time.Second, nil
		case TypeRegisterReject:
			var rj RegisterRejectPayload
			_ = DecodeStrict(env.Payload, &rj)
			return 0, fmt.Errorf("registration rejected: %s (%s)", rj.Reason, rj.Detail)
		case TypeError:
			var ep ErrorPayload
			_ = DecodeStrict(env.Payload, &ep)
			return 0, fmt.Errorf("leader error %s: %s", ep.Code, ep.Message)
		default:
			return 0, fmt.Errorf("unexpected %s during registration", env.Type)
		}
	}
}

func (w *Worker) sendHeartbeat(conn net.Conn) error {
	snap := w.cfg.Snapshot()
	env, err := NewEnvelope(TypeHeartbeat, w.cfg.NodeID, w.now(), HeartbeatPayload{
		CPUPercent: snap.CPUPercent,
		RAMUsedMB:  snap.RAMUsedMB,
		Replicas:   snap.Statuses,
	})
	if err != nil {
		return err
	}
	return w.write(conn, env)
}

// deregister tells the leader this node is going away. It is best effort.
func (w *Worker) deregister(conn net.Conn) {
	env, err := NewEnvelope(TypeDeregister, w.cfg.NodeID, w.now(), DeregisterPayload{Reason: "shutdown"})
	if err != nil {
		return
	}
	_ = w.write(conn, env)
}

func (w *Worker) write(conn net.Conn, env *Envelope) error {
	line, err := Encode(w.cfg.Secret, env)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(w.now().Add(replyTimeout))
	_, err = conn.Write(line)
	return err
}

// verify decodes one leader line and checks its header and signature.
func (w *Worker) verify(line []byte) (*Envelope, error) {
	env, err := Decode(line)
	if err != nil {
		return nil, err
	}
	if err := env.CheckHeader(w.now()); err != nil {
		return nil, err
	}
	if err := env.Verify(w.cfg.Secret); err != nil {
		return nil, err
	}
	return env, nil
}
