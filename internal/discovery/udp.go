package discovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"syscall"
	"time"
)

// Responder answers signed DISCOVER_LEADER probes with a signed LEADER_FOUND.
type Responder struct {
	Secret      []byte
	LeaderID    string
	Address     string
	ControlPort int
	Now         func() time.Time
}

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reply returns the LEADER_FOUND for one datagram, or ok=false when the
// datagram must be ignored. A probe from this leader's own node ID is ignored,
// so the leader does not answer its own single-leader check.
func (r *Responder) Reply(data []byte) ([]byte, bool) {
	probe, err := ParseDiscoverLeader(r.Secret, data)
	if err != nil {
		return nil, false
	}
	if probe.NodeID == r.LeaderID {
		return nil, false
	}
	reply, err := NewLeaderFound(r.Secret, r.LeaderID, r.Address, r.ControlPort, r.now())
	if err != nil {
		return nil, false
	}
	return reply, true
}

// Serve reads datagrams until conn is closed. Oversized datagrams are dropped.
func (r *Responder) Serve(conn net.PacketConn) error {
	buf := make([]byte, MaxDatagramBytes+1)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if n > MaxDatagramBytes {
			continue
		}
		reply, ok := r.Reply(buf[:n])
		if !ok {
			continue
		}
		if _, err := conn.WriteTo(reply, addr); err != nil {
			log.Printf("discovery: reply to %s failed: %v", addr, err)
		}
	}
}

// ListenBroadcast opens a UDP socket with SO_BROADCAST set, bound to an
// ephemeral port, for sending probes to 255.255.255.255.
func ListenBroadcast(ctx context.Context) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = setBroadcast(fd)
			}); err != nil {
				return err
			}
			return serr
		},
	}
	return lc.ListenPacket(ctx, "udp4", ":0")
}

// ListenResponder binds the discovery port on all interfaces.
func ListenResponder(port int) (net.PacketConn, error) {
	return net.ListenPacket("udp4", net.JoinHostPort("", strconv.Itoa(port)))
}

// BroadcastAddr is the LAN broadcast address for a discovery port.
func BroadcastAddr(port int) string {
	return net.JoinHostPort("255.255.255.255", strconv.Itoa(port))
}

// CheckSingleLeader sends one signed probe and waits up to wait for a
// LEADER_FOUND from a different leader. found is true when another leader
// answered. A worker that hears two leaders is handled separately.
func CheckSingleLeader(ctx context.Context, secret []byte, selfID string, discoveryPort int, wait time.Duration) (LeaderFound, bool, error) {
	conn, err := ListenBroadcast(ctx)
	if err != nil {
		return LeaderFound{}, false, err
	}
	defer conn.Close()

	probe, err := NewDiscoverLeader(secret, selfID, time.Now())
	if err != nil {
		return LeaderFound{}, false, err
	}
	dst, err := net.ResolveUDPAddr("udp4", BroadcastAddr(discoveryPort))
	if err != nil {
		return LeaderFound{}, false, err
	}
	if _, err := conn.WriteTo(probe, dst); err != nil {
		return LeaderFound{}, false, fmt.Errorf("single-leader check: send probe: %w", err)
	}

	deadline := time.Now().Add(wait)
	buf := make([]byte, MaxDatagramBytes+1)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return LeaderFound{}, false, err
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return LeaderFound{}, false, nil
			}
			return LeaderFound{}, false, err
		}
		if n > MaxDatagramBytes {
			continue
		}
		reply, err := ParseLeaderFound(secret, buf[:n])
		if err != nil || reply.LeaderID == selfID {
			continue
		}
		return reply, true, nil
	}
}
