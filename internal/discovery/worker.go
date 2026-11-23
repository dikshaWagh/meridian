package discovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/dikshaWagh/meridian/internal/control"
)

// duplicateLeaderGrace is how long a worker keeps listening after its first
// verified reply, to detect a second leader on the LAN.
const duplicateLeaderGrace = 500 * time.Millisecond

// FindLeader broadcasts DISCOVER_LEADER until a verified LEADER_FOUND arrives
// or one round of length round ends. Probes are spaced by exponential backoff
// with jitter, capped at maxBackoff. It returns host:port of the control
// plane. Once it returns, the worker stops broadcasting until it is
// deregistered and back in discovery.
func FindLeader(ctx context.Context, secret []byte, nodeID string, discoveryPort int, round, maxBackoff time.Duration) (string, error) {
	conn, err := ListenBroadcast(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", BroadcastAddr(discoveryPort))
	if err != nil {
		return "", err
	}
	bo := control.NewBackoff(time.Second, maxBackoff)
	deadline := time.Now().Add(round)
	nextSend := time.Now()
	buf := make([]byte, MaxDatagramBytes+1)

	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		now := time.Now()
		if !now.Before(deadline) {
			return "", fmt.Errorf("no leader answered within %s", round)
		}
		if !now.Before(nextSend) {
			probe, err := NewDiscoverLeader(secret, nodeID, now)
			if err != nil {
				return "", err
			}
			if _, err := conn.WriteTo(probe, dst); err != nil {
				return "", fmt.Errorf("send probe: %w", err)
			}
			nextSend = now.Add(bo.Next())
		}

		wait := nextSend
		if deadline.Before(wait) {
			wait = deadline
		}
		if err := conn.SetReadDeadline(wait); err != nil {
			return "", err
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return "", err
		}
		if n > MaxDatagramBytes {
			continue
		}
		reply, err := ParseLeaderFound(secret, buf[:n])
		if err != nil {
			continue
		}
		addr := net.JoinHostPort(reply.Address, strconv.Itoa(reply.ControlPort))
		warnOtherLeaders(conn, secret, reply.LeaderID, buf)
		log.Printf("worker: found leader %s at %s", reply.LeaderID, addr)
		return addr, nil
	}
}

// warnOtherLeaders listens briefly after the first reply. If a different
// leader answers, it logs a warning. The first leader is still used.
func warnOtherLeaders(conn net.PacketConn, secret []byte, first string, buf []byte) {
	_ = conn.SetReadDeadline(time.Now().Add(duplicateLeaderGrace))
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n > MaxDatagramBytes {
			continue
		}
		reply, err := ParseLeaderFound(secret, buf[:n])
		if err != nil || reply.LeaderID == first {
			continue
		}
		log.Printf("worker: warning: leader %s also answered; using %s", reply.LeaderID, first)
	}
}
