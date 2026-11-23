// Package control implements the TCP NDJSON control channel between the leader
// and workers: message envelope, HMAC signing, validation and framing.
package control

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const (
	ProtocolVersion = 1
	MaxLineBytes    = 1 << 20
	MaxClockSkew    = 30 * time.Second
	MaxIDLen        = 64
	replayCacheSize = 1000
)

// Message types.
const (
	TypeRegister       = "REGISTER"
	TypeRegisterAck    = "REGISTER_ACK"
	TypeRegisterReject = "REGISTER_REJECT"
	TypeHeartbeat      = "HEARTBEAT"
	TypeHeartbeatAck   = "HEARTBEAT_ACK"
	TypeReplicaUpdate  = "REPLICA_UPDATE"
	TypeDeregister     = "DEREGISTER"
	TypeError          = "ERROR"
)

// REGISTER_REJECT reasons.
const (
	ReasonBadSignature     = "bad_signature"
	ReasonProtocolMismatch = "protocol_mismatch"
	ReasonInvalidPayload   = "invalid_payload"
	ReasonDuplicateNode    = "duplicate_node"
)

var ErrLineTooLong = errors.New("control: line exceeds 1 MiB")

// Envelope wraps every control message. Sig covers the canonical JSON of the
// envelope with sig removed.
type Envelope struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocol_version"`
	MessageID       string          `json:"message_id"`
	Timestamp       string          `json:"timestamp"`
	NodeID          string          `json:"node_id"`
	Payload         json.RawMessage `json:"payload"`
	Sig             string          `json:"sig"`
}

// Error is a protocol failure. Code is one of the REGISTER_REJECT reasons
// when it applies to a registration.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func protoErr(code, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Signature returns the hex HMAC-SHA256 of the canonical JSON of v with its
// top-level "sig" member removed. Canonical means keys sorted and no
// whitespace. Numbers keep their original text, so a re-encoded value signs the
// same way as the original.
func Signature(secret []byte, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return "", err
	}
	delete(m, "sig")
	canon, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(canon)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifySignature checks sig against v in constant time.
func VerifySignature(secret []byte, v any, sig string) bool {
	want, err := Signature(secret, v)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(want), []byte(sig))
}

// Seal fills in the envelope's sig.
func Seal(secret []byte, env *Envelope) error {
	sig, err := Signature(secret, env)
	if err != nil {
		return err
	}
	env.Sig = sig
	return nil
}

// NewMessageID returns a random 128-bit ID formatted like a UUIDv4.
func NewMessageID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// NewEnvelope builds an unsigned envelope for payload. A nil payload is sent as {}.
func NewEnvelope(msgType, nodeID string, now time.Time, payload any) (*Envelope, error) {
	if payload == nil {
		payload = struct{}{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		Type:            msgType,
		ProtocolVersion: ProtocolVersion,
		MessageID:       NewMessageID(),
		Timestamp:       now.UTC().Format(time.RFC3339),
		NodeID:          nodeID,
		Payload:         raw,
	}, nil
}

// Encode seals env and returns one NDJSON line, including the trailing newline.
func Encode(secret []byte, env *Envelope) ([]byte, error) {
	if err := Seal(secret, env); err != nil {
		return nil, err
	}
	line, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}

// Decode parses one line into an envelope. It does not verify the signature.
func Decode(line []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, protoErr(ReasonInvalidPayload, "malformed envelope: %v", err)
	}
	return &env, nil
}

// CheckHeader checks the parts of the envelope that do not need the secret:
// protocol version, the type and node ID fields, and the timestamp window.
func (e *Envelope) CheckHeader(now time.Time) error {
	if e.ProtocolVersion != ProtocolVersion {
		return protoErr(ReasonProtocolMismatch, "protocol_version %d, want %d", e.ProtocolVersion, ProtocolVersion)
	}
	if err := ValidID(e.NodeID); err != nil {
		return protoErr(ReasonInvalidPayload, "node_id: %v", err)
	}
	if err := ValidID(e.MessageID); err != nil {
		return protoErr(ReasonInvalidPayload, "message_id: %v", err)
	}
	ts, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil {
		return protoErr(ReasonInvalidPayload, "timestamp: %v", err)
	}
	if d := now.Sub(ts); d > MaxClockSkew || d < -MaxClockSkew {
		return protoErr(ReasonInvalidPayload, "timestamp is outside the ±%s window", MaxClockSkew)
	}
	return nil
}

// Verify checks the signature. It must run before anything is trusted.
func (e *Envelope) Verify(secret []byte) error {
	if !VerifySignature(secret, e, e.Sig) {
		return protoErr(ReasonBadSignature, "signature does not match")
	}
	return nil
}

// ValidID accepts non-empty IDs of at most MaxIDLen characters drawn from
// letters, digits, '.', '-' and '_'.
func ValidID(id string) error {
	if id == "" {
		return errors.New("empty")
	}
	if len(id) > MaxIDLen {
		return fmt.Errorf("longer than %d characters", MaxIDLen)
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("%q has characters outside [A-Za-z0-9._-]", id)
		}
	}
	return nil
}

// ValidEndpoint requires an http:// URL with a host.
func ValidEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Host == "" {
		return fmt.Errorf("%q must be an http:// URL with a host", raw)
	}
	return nil
}

// ReadLine reads one newline-terminated line, without the newline. A line
// longer than MaxLineBytes returns ErrLineTooLong, and the caller must close
// the connection because the framing is lost.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > MaxLineBytes+1 {
			return nil, ErrLineTooLong
		}
		switch {
		case err == nil:
			return bytes.TrimSuffix(buf, []byte{'\n'}), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

// ReplayCache remembers the last replayCacheSize message IDs of one node.
// Adding an ID that is already present returns false.
type ReplayCache struct {
	seen map[string]struct{}
	ring []string
	next int
}

func NewReplayCache() *ReplayCache {
	return &ReplayCache{seen: make(map[string]struct{}, replayCacheSize), ring: make([]string, 0, replayCacheSize)}
}

// Add records id. It returns false when id was already seen.
func (c *ReplayCache) Add(id string) bool {
	if _, ok := c.seen[id]; ok {
		return false
	}
	if len(c.ring) < replayCacheSize {
		c.ring = append(c.ring, id)
	} else {
		delete(c.seen, c.ring[c.next])
		c.ring[c.next] = id
		c.next = (c.next + 1) % replayCacheSize
	}
	c.seen[id] = struct{}{}
	return true
}

// Len reports how many IDs are cached. It never exceeds replayCacheSize.
func (c *ReplayCache) Len() int { return len(c.seen) }
