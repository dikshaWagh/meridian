package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Headers that carry the leader's signature on forwarded inference requests.
const (
	HeaderTimestamp = "X-Meridian-Timestamp"
	HeaderSignature = "X-Meridian-Signature"
)

// forwardSignature is HMAC-SHA256 over method, path, timestamp and the SHA-256
// of the body, so a captured request cannot be replayed with a different body
// or path.
func forwardSignature(secret []byte, method, path, ts string, body []byte) string {
	sum := sha256.Sum256(body)
	msg := strings.Join([]string{method, path, ts, hex.EncodeToString(sum[:])}, "\n")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignForward returns the timestamp and signature headers for one forwarded request.
func SignForward(secret []byte, method, path string, body []byte, now time.Time) (ts, sig string) {
	ts = now.UTC().Format(time.RFC3339)
	return ts, forwardSignature(secret, method, path, ts, body)
}

// VerifyForward checks a forwarded request. It enforces the same ±30 s window
// as the control channel.
func VerifyForward(secret []byte, method, path string, body []byte, ts, sig string, now time.Time) error {
	if ts == "" || sig == "" {
		return errors.New("missing forwarding signature")
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return errors.New("bad timestamp")
	}
	if d := now.Sub(t); d > MaxClockSkew || d < -MaxClockSkew {
		return errors.New("timestamp outside the allowed window")
	}
	want := forwardSignature(secret, method, path, ts, body)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("signature does not match")
	}
	return nil
}
