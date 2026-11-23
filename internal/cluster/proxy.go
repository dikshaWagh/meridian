package cluster

import (
	"bytes"
	"context"
	"net/http"
	"time"
)

// upstreamClient has no overall timeout, because a generation can run for
// minutes. The response header timeout still catches a wedged upstream.
var upstreamClient = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 5 * time.Minute}}

func postJSON(ctx context.Context, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// relay copies an upstream reply to w and flushes after each chunk, so
// streamed tokens reach the client as they are produced.
func relay(w http.ResponseWriter, resp *http.Response) {
	for _, h := range []string{"Content-Type", "Cache-Control", "X-Request-ID"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
