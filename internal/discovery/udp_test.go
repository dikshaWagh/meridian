package discovery

import (
	"net"
	"testing"
	"time"
)

var secret = []byte("test-secret")

func responder() *Responder {
	return &Responder{Secret: secret, LeaderID: "leader-01", Address: "192.168.1.10", ControlPort: 9000}
}

func TestSignedProbeGetsSignedReply(t *testing.T) {
	probe, err := NewDiscoverLeader(secret, "node-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reply, ok := responder().Reply(probe)
	if !ok {
		t.Fatal("valid probe ignored")
	}
	got, err := ParseLeaderFound(secret, reply)
	if err != nil {
		t.Fatalf("reply does not verify: %v", err)
	}
	if got.LeaderID != "leader-01" || got.ControlPort != 9000 || got.Address != "192.168.1.10" {
		t.Fatalf("reply = %+v", got)
	}
}

func TestBadlySignedProbeIgnored(t *testing.T) {
	probe, _ := NewDiscoverLeader([]byte("wrong"), "node-a", time.Now())
	if _, ok := responder().Reply(probe); ok {
		t.Fatal("badly signed probe was answered")
	}
}

func TestOwnProbeIgnored(t *testing.T) {
	probe, _ := NewDiscoverLeader(secret, "leader-01", time.Now())
	if _, ok := responder().Reply(probe); ok {
		t.Fatal("leader answered its own probe")
	}
}

func TestMalformedAndWrongTypeIgnored(t *testing.T) {
	for _, data := range []string{"not json", `{"type":"LEADER_FOUND"}`, `{}`} {
		if _, ok := responder().Reply([]byte(data)); ok {
			t.Fatalf("%q was answered", data)
		}
	}
}

func TestBadlySignedReplyRejectedByWorker(t *testing.T) {
	reply, _ := NewLeaderFound([]byte("rogue"), "rogue-01", "10.0.0.66", 9000, time.Now())
	if _, err := ParseLeaderFound(secret, reply); err == nil {
		t.Fatal("rogue LEADER_FOUND accepted")
	}
}

func TestOversizedDatagramRejected(t *testing.T) {
	big := make([]byte, MaxDatagramBytes+1)
	if _, err := ParseDiscoverLeader(secret, big); err == nil {
		t.Fatal("oversized datagram accepted")
	}
}

func TestResponderOverUDP(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() { _ = responder().Serve(conn) }()

	client, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	probe, _ := NewDiscoverLeader(secret, "node-a", time.Now())
	if _, err := client.WriteTo(probe, conn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := client.ReadFrom(buf)
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	if _, err := ParseLeaderFound(secret, buf[:n]); err != nil {
		t.Fatalf("reply rejected: %v", err)
	}

	// A badly signed probe gets no reply.
	bad, _ := NewDiscoverLeader([]byte("wrong"), "node-a", time.Now())
	_, _ = client.WriteTo(bad, conn.LocalAddr())
	_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := client.ReadFrom(buf); err == nil {
		t.Fatalf("got %d bytes in reply to a bad probe", n)
	}
}
