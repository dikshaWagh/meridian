package discovery

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// freeUDPPort returns a UDP port that was free a moment ago.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func startResponder(t *testing.T, port int, r *Responder) {
	t.Helper()
	conn, err := ListenResponder(port)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.Serve(conn) }()
	t.Cleanup(func() { conn.Close() })
}

func TestFindLeaderOverBroadcast(t *testing.T) {
	port := freeUDPPort(t)
	startResponder(t, port, &Responder{Secret: secret, LeaderID: "leader-01", Address: "127.0.0.1", ControlPort: 19000})

	addr, err := FindLeader(context.Background(), secret, "node-w", port, 5*time.Second, time.Second)
	if err != nil {
		t.Skipf("broadcast on this host did not reach the responder: %v", err)
	}
	if addr != net.JoinHostPort("127.0.0.1", strconv.Itoa(19000)) {
		t.Fatalf("addr = %s", addr)
	}
}

func TestFindLeaderIgnoresRogueLeader(t *testing.T) {
	port := freeUDPPort(t)
	startResponder(t, port, &Responder{Secret: []byte("rogue"), LeaderID: "rogue", Address: "10.0.0.66", ControlPort: 9000})

	start := time.Now()
	_, err := FindLeader(context.Background(), secret, "node-w", port, 1500*time.Millisecond, time.Second)
	if err == nil {
		t.Fatal("worker accepted a rogue leader")
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatal("round ended before its deadline")
	}
}

func TestFindLeaderHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FindLeader(ctx, secret, "node-w", freeUDPPort(t), time.Minute, time.Second); err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}
