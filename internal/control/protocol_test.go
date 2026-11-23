package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-secret")
var now = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func TestSignatureIsStableAndSensitive(t *testing.T) {
	a := map[string]any{"type": "HEARTBEAT", "node_id": "n1", "payload": map[string]any{"x": 1}}
	s1, err := Signature(secret, a)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := Signature(secret, a)
	if s1 != s2 {
		t.Fatal("same input gave different signatures")
	}
	b := map[string]any{"payload": map[string]any{"x": 1}, "node_id": "n1", "type": "HEARTBEAT"}
	if s3, _ := Signature(secret, b); s3 != s1 {
		t.Fatal("key order changed the signature; canonical JSON must sort keys")
	}
	a["node_id"] = "n2"
	if s4, _ := Signature(secret, a); s4 == s1 {
		t.Fatal("changed field produced the same signature")
	}
}

func TestSignatureIgnoresSigField(t *testing.T) {
	a := map[string]any{"type": "X", "sig": "anything"}
	b := map[string]any{"type": "X", "sig": ""}
	sa, _ := Signature(secret, a)
	sb, _ := Signature(secret, b)
	if sa != sb {
		t.Fatal("sig member must not be part of the signed bytes")
	}
}

func TestWrongSecretRejected(t *testing.T) {
	env, _ := NewEnvelope(TypeHeartbeat, "n1", now, struct{}{})
	if err := Seal(secret, env); err != nil {
		t.Fatal(err)
	}
	if err := env.Verify([]byte("other")); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if err := env.Verify(secret); err != nil {
		t.Fatalf("right secret rejected: %v", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	env, _ := NewEnvelope(TypeRegister, "node-1", now, RegisterAckPayload{LeaseSeconds: 15})
	line, err := Encode(secret, env)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(line, []byte("\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("line must be one JSON object plus a newline: %q", line)
	}
	got, err := Decode(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(secret); err != nil {
		t.Fatalf("round-trip signature failed: %v", err)
	}
}

func TestCheckHeaderTimestampWindow(t *testing.T) {
	cases := []struct {
		offset time.Duration
		ok     bool
	}{
		{0, true},
		{29 * time.Second, true},
		{-29 * time.Second, true},
		{31 * time.Second, false},
		{-31 * time.Second, false},
	}
	for _, c := range cases {
		env := &Envelope{ProtocolVersion: 1, MessageID: NewMessageID(), NodeID: "n1", Timestamp: now.Add(c.offset).UTC().Format(time.RFC3339)}
		err := env.CheckHeader(now)
		if (err == nil) != c.ok {
			t.Fatalf("offset %s: err=%v, want ok=%v", c.offset, err, c.ok)
		}
	}
}

func TestCheckHeaderProtocolVersion(t *testing.T) {
	env := &Envelope{ProtocolVersion: 2, MessageID: NewMessageID(), NodeID: "n1", Timestamp: now.UTC().Format(time.RFC3339)}
	var pe *Error
	if err := env.CheckHeader(now); !errors.As(err, &pe) || pe.Code != ReasonProtocolMismatch {
		t.Fatalf("err = %v, want protocol_mismatch", err)
	}
}

func TestCheckHeaderRejectsMissingNodeID(t *testing.T) {
	env := &Envelope{ProtocolVersion: 1, MessageID: NewMessageID(), Timestamp: now.UTC().Format(time.RFC3339)}
	if err := env.CheckHeader(now); err == nil {
		t.Fatal("missing node_id accepted")
	}
}

func TestReplayCacheRejectsDuplicatesAndIsBounded(t *testing.T) {
	c := NewReplayCache()
	if !c.Add("a") {
		t.Fatal("first add rejected")
	}
	if c.Add("a") {
		t.Fatal("duplicate accepted")
	}
	for i := 0; i < 1500; i++ {
		c.Add(fmt.Sprintf("id-%d", i))
	}
	if c.Len() != replayCacheSize {
		t.Fatalf("cache size %d, want %d", c.Len(), replayCacheSize)
	}
	// "id-1499" is in the cache, so it is still rejected.
	if c.Add("id-1499") {
		t.Fatal("recent id accepted again")
	}
	// "a" and "id-0" were evicted, so they are accepted again.
	if !c.Add("a") {
		t.Fatal("evicted id still rejected")
	}
}

func TestReadLineFramingAndLimit(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("one\ntwo\n"))
	for _, want := range []string{"one", "two"} {
		got, err := ReadLine(r)
		if err != nil || string(got) != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	long := strings.Repeat("a", MaxLineBytes+10) + "\n"
	if _, err := ReadLine(bufio.NewReader(strings.NewReader(long))); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	var p RegisterAckPayload
	if err := DecodeStrict(json.RawMessage(`{"lease_seconds":15,"extra":1}`), &p); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestValidID(t *testing.T) {
	for _, bad := range []string{"", "has space", "slash/no", strings.Repeat("x", 65)} {
		if ValidID(bad) == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := ValidID("llama-8b-abc123-1"); err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
}

func TestValidEndpoint(t *testing.T) {
	if ValidEndpoint("http://192.168.1.11:8080/replicas/x") != nil {
		t.Fatal("valid endpoint rejected")
	}
	for _, bad := range []string{"https://x:1", "ftp://x", "http://", "not a url"} {
		if ValidEndpoint(bad) == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func validRegister() RegisterPayload {
	return RegisterPayload{
		Address:  "192.168.1.11",
		HTTPPort: 8080,
		Replicas: []ReplicaInfo{{
			ID: "llama-8b-01", Model: "llama-8b", Instance: 1, Kind: "llm", Runtime: "llama.cpp",
			Endpoint: "http://192.168.1.11:8080/replicas/llama-8b-01", State: "ready",
		}},
	}
}

func TestRegisterPayloadValidation(t *testing.T) {
	p := validRegister()
	if err := p.Validate(); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	mutations := map[string]func(*RegisterPayload){
		"bad address":        func(p *RegisterPayload) { p.Address = "nope" },
		"bad port":           func(p *RegisterPayload) { p.HTTPPort = 70000 },
		"empty model":        func(p *RegisterPayload) { p.Replicas[0].Model = "" },
		"long model":         func(p *RegisterPayload) { p.Replicas[0].Model = strings.Repeat("m", 65) },
		"bad endpoint":       func(p *RegisterPayload) { p.Replicas[0].Endpoint = "https://x" },
		"unknown state":      func(p *RegisterPayload) { p.Replicas[0].State = "napping" },
		"bad kind":           func(p *RegisterPayload) { p.Replicas[0].Kind = "image" },
		"zero instance":      func(p *RegisterPayload) { p.Replicas[0].Instance = 0 },
		"duplicate replicas": func(p *RegisterPayload) { p.Replicas = append(p.Replicas, p.Replicas[0]) },
	}
	for name, mutate := range mutations {
		p := validRegister()
		p.Replicas = append([]ReplicaInfo(nil), validRegister().Replicas...)
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
