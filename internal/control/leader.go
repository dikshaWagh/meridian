package control

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/dikshaWagh/meridian/internal/registry"
)

const writeTimeout = 5 * time.Second

// LeaderConfig holds what the leader's control listener needs.
type LeaderConfig struct {
	NodeID       string
	Secret       []byte
	LeaseSeconds int
	Now          func() time.Time
}

// Leader accepts worker connections, validates every message and updates the
// registry. It holds no scheduling logic.
type Leader struct {
	cfg LeaderConfig
	reg registry.Registry

	mu       sync.Mutex
	sessions map[string]net.Conn // live connection per node ID
	conns    map[net.Conn]struct{}
	replay   map[string]*ReplayCache
	wg       sync.WaitGroup
}

func NewLeader(cfg LeaderConfig, reg registry.Registry) *Leader {
	return &Leader{
		cfg:      cfg,
		reg:      reg,
		sessions: map[string]net.Conn{},
		conns:    map[net.Conn]struct{}{},
		replay:   map[string]*ReplayCache{},
	}
}

func (l *Leader) now() time.Time {
	if l.cfg.Now != nil {
		return l.cfg.Now()
	}
	return time.Now()
}

// Serve accepts connections until ctx is cancelled. It then closes every
// connection and waits for the handlers to return.
func (l *Leader) Serve(ctx context.Context, ln net.Listener) error {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		_ = ln.Close()
		l.closeAll()
	}()
	defer close(stop)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("control: accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		l.track(conn)
		l.wg.Add(1)
		go l.serveConn(conn)
	}
	l.wg.Wait()
	return nil
}

func (l *Leader) track(conn net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns[conn] = struct{}{}
}

func (l *Leader) untrack(conn net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.conns, conn)
}

func (l *Leader) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := range l.conns {
		_ = c.Close()
	}
}

func (l *Leader) serveConn(conn net.Conn) {
	defer l.wg.Done()
	defer l.untrack(conn)
	defer conn.Close()

	r := bufio.NewReader(conn)
	nodeID := ""
	for {
		line, err := ReadLine(r)
		if err != nil {
			if errors.Is(err, ErrLineTooLong) {
				l.sendError(conn, "line_too_long", "control lines are limited to 1 MiB")
			}
			break
		}
		if nodeID == "" {
			id, ok := l.register(conn, line)
			if !ok {
				break
			}
			nodeID = id
			continue
		}
		if !l.handle(conn, nodeID, line) {
			break
		}
	}
	if nodeID != "" {
		l.disconnect(nodeID, conn)
	}
}

// register handles the first message on a connection. It returns the node ID
// when the registration is accepted.
func (l *Leader) register(conn net.Conn, line []byte) (string, bool) {
	env, err := Decode(line)
	if err != nil {
		l.rejectRegister(conn, err)
		return "", false
	}
	if env.Type != TypeRegister {
		l.sendError(conn, "expected_register", "the first message on a connection must be REGISTER")
		return "", false
	}
	if err := env.CheckHeader(l.now()); err != nil {
		l.rejectRegister(conn, err)
		return "", false
	}
	if err := env.Verify(l.cfg.Secret); err != nil {
		l.rejectRegister(conn, err)
		return "", false
	}
	if !l.replayFor(env.NodeID).Add(env.MessageID) {
		l.rejectRegister(conn, protoErr(ReasonInvalidPayload, "message_id %s was already used", env.MessageID))
		return "", false
	}
	var p RegisterPayload
	if err := DecodeStrict(env.Payload, &p); err != nil {
		l.rejectRegister(conn, err)
		return "", false
	}
	if err := p.Validate(); err != nil {
		l.rejectRegister(conn, err)
		return "", false
	}

	now := l.now()
	node := registry.NodeRecord{
		NodeID:        env.NodeID,
		Address:       p.Address,
		HTTPPort:      p.HTTPPort,
		ControlConn:   conn,
		State:         registry.NodeOnline,
		LastHeartbeat: now,
		RegisteredAt:  now,
		Hardware:      hardwareOf(p.Hardware),
	}
	replicas := make([]registry.ReplicaRecord, 0, len(p.Replicas))
	for _, r := range p.Replicas {
		replicas = append(replicas, registry.ReplicaRecord{
			ReplicaID:     r.ID,
			NodeID:        env.NodeID,
			Model:         r.Model,
			Instance:      r.Instance,
			ModelVersion:  r.ModelVersion,
			Kind:          r.Kind,
			Runtime:       r.Runtime,
			Endpoint:      r.Endpoint,
			ContextWindow: r.ContextWindow,
			MaxConcurrent: r.MaxConcurrent,
			State:         registry.ReplicaState(r.State),
			LastCheck:     now,
		})
	}

	l.mu.Lock()
	old, live := l.sessions[env.NodeID]
	if live && old != conn {
		if prev, ok := l.reg.Node(env.NodeID); ok && prev.State == registry.NodeOnline {
			l.mu.Unlock()
			l.rejectRegister(conn, protoErr(ReasonDuplicateNode, "node %s is already registered from another connection", env.NodeID))
			return "", false
		}
		// The previous connection belongs to a node that has gone offline.
		// Close it; its handler will see that it no longer owns the session.
		_ = old.Close()
	}
	if err := l.reg.ReplaceNode(node, replicas); err != nil {
		l.mu.Unlock()
		l.rejectRegister(conn, protoErr(ReasonInvalidPayload, "%v", err))
		return "", false
	}
	l.sessions[env.NodeID] = conn
	l.mu.Unlock()

	log.Printf("control: node %s registered (%d replicas) from %s", env.NodeID, len(replicas), conn.RemoteAddr())
	if err := l.send(conn, TypeRegisterAck, RegisterAckPayload{LeaseSeconds: l.cfg.LeaseSeconds}); err != nil {
		return "", false
	}
	return env.NodeID, true
}

// handle processes one message after registration. It returns false when the
// connection must close.
func (l *Leader) handle(conn net.Conn, nodeID string, line []byte) bool {
	env, err := Decode(line)
	if err != nil {
		l.sendErr(conn, err)
		return false
	}
	if env.NodeID != nodeID {
		l.sendError(conn, "node_mismatch", "node_id does not match this connection's node")
		return false
	}
	if err := env.CheckHeader(l.now()); err != nil {
		l.sendErr(conn, err)
		return false
	}
	if err := env.Verify(l.cfg.Secret); err != nil {
		l.sendErr(conn, err)
		return false
	}
	if !l.replayFor(nodeID).Add(env.MessageID) {
		l.sendError(conn, ReasonInvalidPayload, "replayed message_id")
		return false
	}

	now := l.now()
	switch env.Type {
	case TypeHeartbeat:
		var p HeartbeatPayload
		if err := DecodeStrict(env.Payload, &p); err != nil {
			l.sendErr(conn, err)
			return false
		}
		if err := p.Validate(); err != nil {
			l.sendErr(conn, err)
			return false
		}
		if err := l.reg.UpdateNodeStats(nodeID, registry.NodeStats{At: now, CPUPercent: p.CPUPercent, RAMUsedMB: p.RAMUsedMB}); err != nil {
			l.sendError(conn, "unknown_node", "node is not registered; register again")
			return false
		}
		for _, r := range p.Replicas {
			if !l.ownsReplica(nodeID, r.ID) {
				continue
			}
			_ = l.reg.UpdateReplicaState(r.ID, registry.ReplicaState(r.State))
			_ = l.reg.UpdateReplicaStats(r.ID, replicaStats(r, now))
		}
		return l.send(conn, TypeHeartbeatAck, struct{}{}) == nil

	case TypeReplicaUpdate:
		var p ReplicaStatus
		if err := DecodeStrict(env.Payload, &p); err != nil {
			l.sendErr(conn, err)
			return false
		}
		if err := p.validate(); err != nil {
			l.sendErr(conn, protoErr(ReasonInvalidPayload, "%v", err))
			return false
		}
		if !l.ownsReplica(nodeID, p.ID) {
			l.sendError(conn, "unknown_replica", "replica is not registered on this node; register again")
			return false
		}
		_ = l.reg.UpdateReplicaState(p.ID, registry.ReplicaState(p.State))
		_ = l.reg.UpdateReplicaStats(p.ID, replicaStats(p, now))
		return true

	case TypeDeregister:
		l.reg.MarkNodeOffline(nodeID, now)
		return false

	default:
		l.sendError(conn, "unexpected_type", "message type "+env.Type+" is not accepted from a registered node")
		return false
	}
}

func (l *Leader) ownsReplica(nodeID, replicaID string) bool {
	r, ok := l.reg.Replica(replicaID)
	return ok && r.NodeID == nodeID
}

// disconnect marks the node offline, but only when this connection still owns
// the node. A newer registration on another connection keeps the node online.
func (l *Leader) disconnect(nodeID string, conn net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sessions[nodeID] != conn {
		return
	}
	delete(l.sessions, nodeID)
	l.reg.MarkNodeOffline(nodeID, l.now())
	log.Printf("control: node %s disconnected; marked offline", nodeID)
}

func (l *Leader) replayFor(nodeID string) *ReplayCache {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.replay[nodeID]
	if !ok {
		c = NewReplayCache()
		l.replay[nodeID] = c
	}
	return c
}

func (l *Leader) send(conn net.Conn, msgType string, payload any) error {
	env, err := NewEnvelope(msgType, l.cfg.NodeID, l.now(), payload)
	if err != nil {
		return err
	}
	line, err := Encode(l.cfg.Secret, env)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = conn.Write(line)
	return err
}

func (l *Leader) rejectRegister(conn net.Conn, err error) {
	reason, detail := ReasonInvalidPayload, err.Error()
	var pe *Error
	if errors.As(err, &pe) {
		reason, detail = pe.Code, pe.Msg
	}
	_ = l.send(conn, TypeRegisterReject, RegisterRejectPayload{Reason: reason, Detail: detail})
}

func (l *Leader) sendErr(conn net.Conn, err error) {
	var pe *Error
	if errors.As(err, &pe) {
		l.sendError(conn, pe.Code, pe.Msg)
		return
	}
	l.sendError(conn, ReasonInvalidPayload, err.Error())
}

func (l *Leader) sendError(conn net.Conn, code, msg string) {
	_ = l.send(conn, TypeError, ErrorPayload{Code: code, Message: msg})
}

func hardwareOf(h HardwareInfo) registry.Hardware {
	out := registry.Hardware{CPUCores: h.CPUCores, RAMTotalMB: h.RAMTotalMB}
	if h.CPUCores == 0 {
		out.CPUCores = runtime.NumCPU()
	}
	for _, g := range h.GPUs {
		out.GPUs = append(out.GPUs, registry.GPU{Name: g.Name, VRAMMB: g.VRAMMB})
	}
	return out
}

func replicaStats(r ReplicaStatus, now time.Time) registry.ReplicaStats {
	return registry.ReplicaStats{
		QueueDepth:        r.QueueDepth,
		Served:            r.Served,
		EstTokensInFlight: r.EstTokensInFlight,
		TokPerSec:         r.TokPerSec,
		LastCheck:         now,
		LastError:         r.LastError,
	}
}
