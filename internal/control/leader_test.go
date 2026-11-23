package control

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/registry"
)

type testLeader struct {
	t    *testing.T
	reg  registry.Registry
	addr string
	stop func()
}

func startLeader(t *testing.T) *testLeader {
	t.Helper()
	reg := registry.New()
	l := NewLeader(LeaderConfig{NodeID: "leader-01", Secret: secret, LeaseSeconds: 15}, reg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.Serve(ctx, ln); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("leader did not shut down")
			}
		})
	}
	t.Cleanup(stop)
	return &testLeader{t: t, reg: reg, addr: ln.Addr().String(), stop: stop}
}

type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (tl *testLeader) dial() *client {
	tl.t.Helper()
	conn, err := net.DialTimeout("tcp", tl.addr, 2*time.Second)
	if err != nil {
		tl.t.Fatal(err)
	}
	tl.t.Cleanup(func() { conn.Close() })
	return &client{t: tl.t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *client) send(msgType, nodeID string, payload any) {
	c.t.Helper()
	env, err := NewEnvelope(msgType, nodeID, time.Now(), payload)
	if err != nil {
		c.t.Fatal(err)
	}
	line, err := Encode(secret, env)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(line); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) sendRaw(line string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) recv() *Envelope {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := ReadLine(c.r)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	env, err := Decode(line)
	if err != nil {
		c.t.Fatal(err)
	}
	return env
}

func (c *client) recvRejectReason() string {
	c.t.Helper()
	env := c.recv()
	if env.Type != TypeRegisterReject {
		c.t.Fatalf("got %s, want REGISTER_REJECT", env.Type)
	}
	var p RegisterRejectPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		c.t.Fatal(err)
	}
	return p.Reason
}

func (c *client) expectEOF() {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := ReadLine(c.r); err == nil {
		c.t.Fatal("expected the connection to close")
	}
}

func (tl *testLeader) waitFor(cond func() bool) {
	tl.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tl.t.Fatal("condition not reached")
}

func registerPayload(nodeID string) RegisterPayload {
	p := validRegister()
	p.Replicas[0].ID = nodeID + "-llama-1"
	p.Replicas[0].Endpoint = "http://192.168.1.11:8080/replicas/" + p.Replicas[0].ID
	return p
}

func TestRegisterAcceptedThenStatusListsNodeAndReplicas(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeRegister, "node-a", registerPayload("node-a"))
	ack := c.recv()
	if ack.Type != TypeRegisterAck {
		t.Fatalf("got %s, want REGISTER_ACK", ack.Type)
	}
	if err := ack.Verify(secret); err != nil {
		t.Fatalf("leader's ack is not signed correctly: %v", err)
	}
	n, ok := tl.reg.Node("node-a")
	if !ok || n.State != registry.NodeOnline || n.ControlConn == nil {
		t.Fatalf("node not registered: %+v ok=%v", n, ok)
	}
	if reps := tl.reg.ReplicasOnNode("node-a"); len(reps) != 1 || reps[0].State != registry.ReplicaReady {
		t.Fatalf("replicas = %+v", reps)
	}
}

func TestRegisterBadSignatureRejected(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	env, _ := NewEnvelope(TypeRegister, "node-a", time.Now(), registerPayload("node-a"))
	line, _ := Encode([]byte("wrong-secret"), env)
	c.conn.Write(line)
	if reason := c.recvRejectReason(); reason != ReasonBadSignature {
		t.Fatalf("reason = %s", reason)
	}
	c.expectEOF()
	if _, ok := tl.reg.Node("node-a"); ok {
		t.Fatal("node registered despite bad signature")
	}
}

func TestRegisterProtocolMismatchRejected(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	env, _ := NewEnvelope(TypeRegister, "node-a", time.Now(), registerPayload("node-a"))
	env.ProtocolVersion = 2
	line, _ := Encode(secret, env)
	c.conn.Write(line)
	if reason := c.recvRejectReason(); reason != ReasonProtocolMismatch {
		t.Fatalf("reason = %s", reason)
	}
}

func TestRegisterInvalidPayloadRejected(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	bad := registerPayload("node-a")
	bad.Replicas[0].State = "napping"
	c.send(TypeRegister, "node-a", bad)
	if reason := c.recvRejectReason(); reason != ReasonInvalidPayload {
		t.Fatalf("reason = %s", reason)
	}
}

func TestDuplicateMessageIDRejected(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	env, _ := NewEnvelope(TypeRegister, "node-a", time.Now(), registerPayload("node-a"))
	line, _ := Encode(secret, env)
	c.conn.Write(line)
	if ack := c.recv(); ack.Type != TypeRegisterAck {
		t.Fatalf("first register: got %s", ack.Type)
	}
	// Same envelope again on a new connection: the replay cache must catch it.
	c2 := tl.dial()
	c2.conn.Write(line)
	if reason := c2.recvRejectReason(); reason != ReasonInvalidPayload {
		t.Fatalf("replay reason = %s", reason)
	}
}

func TestStaleTimestampRejected(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	env, _ := NewEnvelope(TypeRegister, "node-a", time.Now().Add(-time.Minute), registerPayload("node-a"))
	line, _ := Encode(secret, env)
	c.conn.Write(line)
	if reason := c.recvRejectReason(); reason != ReasonInvalidPayload {
		t.Fatalf("reason = %s", reason)
	}
}

func TestDuplicateNodeWhileOnlineRejected(t *testing.T) {
	tl := startLeader(t)
	first := tl.dial()
	first.send(TypeRegister, "node-a", registerPayload("node-a"))
	first.recv()

	second := tl.dial()
	second.send(TypeRegister, "node-a", registerPayload("node-a"))
	if reason := second.recvRejectReason(); reason != ReasonDuplicateNode {
		t.Fatalf("reason = %s", reason)
	}
}

func TestReturningNodeReplacesOldSession(t *testing.T) {
	tl := startLeader(t)
	first := tl.dial()
	first.send(TypeRegister, "node-a", registerPayload("node-a"))
	first.recv()
	// Simulate the lease expiring: the node is offline but the old socket is still open.
	tl.reg.MarkNodeOffline("node-a", time.Now())

	second := tl.dial()
	second.send(TypeRegister, "node-a", registerPayload("node-a"))
	if ack := second.recv(); ack.Type != TypeRegisterAck {
		t.Fatalf("got %s, want REGISTER_ACK", ack.Type)
	}
	first.expectEOF()
	if n := len(tl.reg.Nodes()); n != 1 {
		t.Fatalf("nodes = %d, want 1 (no duplicate entry)", n)
	}
	if n, _ := tl.reg.Node("node-a"); n.State != registry.NodeOnline {
		t.Fatalf("state = %s, want online", n.State)
	}
}

func TestHeartbeatUpdatesReplicaState(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeRegister, "node-a", registerPayload("node-a"))
	c.recv()

	hb := HeartbeatPayload{CPUPercent: 37.5, RAMUsedMB: 9120, Replicas: []ReplicaStatus{{
		ID: "node-a-llama-1", State: "unhealthy", InFlight: 1, Served: 412, LastError: "boom",
	}}}
	c.send(TypeHeartbeat, "node-a", hb)
	if ack := c.recv(); ack.Type != TypeHeartbeatAck {
		t.Fatalf("got %s, want HEARTBEAT_ACK", ack.Type)
	}
	rep, _ := tl.reg.Replica("node-a-llama-1")
	if rep.State != registry.ReplicaUnhealthy || rep.Healthy || rep.Served != 412 || rep.LastError != "boom" {
		t.Fatalf("replica = %+v", rep)
	}
	if n, _ := tl.reg.Node("node-a"); n.CPUPercent != 37.5 || n.RAMUsedMB != 9120 {
		t.Fatalf("node stats = %+v", n)
	}
}

func TestHeartbeatForReplicaOfAnotherNodeIsIgnored(t *testing.T) {
	tl := startLeader(t)
	a := tl.dial()
	a.send(TypeRegister, "node-a", registerPayload("node-a"))
	a.recv()
	b := tl.dial()
	b.send(TypeRegister, "node-b", registerPayload("node-b"))
	b.recv()

	b.send(TypeHeartbeat, "node-b", HeartbeatPayload{Replicas: []ReplicaStatus{{ID: "node-a-llama-1", State: "stopped"}}})
	b.recv()
	if rep, _ := tl.reg.Replica("node-a-llama-1"); rep.State != registry.ReplicaReady {
		t.Fatalf("node-b changed node-a's replica to %s", rep.State)
	}
}

func TestReplicaUpdateChangesState(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeRegister, "node-a", registerPayload("node-a"))
	c.recv()
	c.send(TypeReplicaUpdate, "node-a", ReplicaStatus{ID: "node-a-llama-1", State: "loading"})
	tl.waitFor(func() bool {
		rep, _ := tl.reg.Replica("node-a-llama-1")
		return rep.State == registry.ReplicaLoading
	})
}

func TestDeregisterMarksNodeOffline(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeRegister, "node-a", registerPayload("node-a"))
	c.recv()
	c.send(TypeDeregister, "node-a", DeregisterPayload{Reason: "shutdown"})
	tl.waitFor(func() bool {
		n, _ := tl.reg.Node("node-a")
		return n.State == registry.NodeOffline
	})
	if rep, _ := tl.reg.Replica("node-a-llama-1"); rep.State != registry.ReplicaUnavailable {
		t.Fatalf("replica state = %s, want unavailable", rep.State)
	}
}

func TestConnectionDropMarksNodeOffline(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeRegister, "node-a", registerPayload("node-a"))
	c.recv()
	c.conn.Close()
	tl.waitFor(func() bool {
		n, _ := tl.reg.Node("node-a")
		return n.State == registry.NodeOffline
	})
}

func TestHeartbeatBeforeRegisterIsRefused(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	c.send(TypeHeartbeat, "node-a", HeartbeatPayload{})
	if env := c.recv(); env.Type != TypeError {
		t.Fatalf("got %s, want ERROR", env.Type)
	}
	c.expectEOF()
}

func TestOversizedLineClosesConnection(t *testing.T) {
	tl := startLeader(t)
	c := tl.dial()
	big := make([]byte, MaxLineBytes+100)
	for i := range big {
		big[i] = 'a'
	}
	c.conn.Write(append(big, '\n'))
	if env := c.recv(); env.Type != TypeError {
		t.Fatalf("got %s, want ERROR before close", env.Type)
	}
	c.expectEOF()
}
