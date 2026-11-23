package control

import (
	"testing"
	"time"
)

func TestForwardSignatureRoundTrip(t *testing.T) {
	body := []byte(`{"model":"m"}`)
	ts, sig := SignForward(secret, "POST", "/replicas/r-1/v1/chat/completions", body, now)
	if err := VerifyForward(secret, "POST", "/replicas/r-1/v1/chat/completions", body, ts, sig, now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestForwardSignatureBindsBodyPathAndSecret(t *testing.T) {
	body := []byte(`{"model":"m"}`)
	path := "/replicas/r-1/v1/chat/completions"
	ts, sig := SignForward(secret, "POST", path, body, now)
	cases := map[string]func() error{
		"changed body": func() error {
			return VerifyForward(secret, "POST", path, []byte(`{"model":"x"}`), ts, sig, now)
		},
		"changed path": func() error {
			return VerifyForward(secret, "POST", "/replicas/r-2/v1/chat/completions", body, ts, sig, now)
		},
		"wrong secret": func() error {
			return VerifyForward([]byte("other"), "POST", path, body, ts, sig, now)
		},
		"stale": func() error {
			return VerifyForward(secret, "POST", path, body, ts, sig, now.Add(time.Minute))
		},
		"missing": func() error {
			return VerifyForward(secret, "POST", path, body, "", "", now)
		},
	}
	for name, f := range cases {
		if f() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
