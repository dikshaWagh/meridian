package balancer

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestStreamUsageReaderCapturesUsageAndText(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Ru\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"dra\"}}]}\r\n\r\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":2,\"total_tokens\":14}}\n\n" +
		"data: [DONE]\n\n"

	usage := &requestUsage{}
	// One byte at a time, so every line is split across reads.
	src := io.NopCloser(&oneByteReader{data: []byte(sse)})
	r := &streamUsageReader{src: src, usage: usage}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, []byte(sse)) {
		t.Fatal("stream body was altered")
	}
	if !usage.ok || usage.in != 12 || usage.out != 2 {
		t.Fatalf("usage not captured: %+v", usage)
	}
	if usage.firstTok.IsZero() {
		t.Fatal("first-token time not recorded")
	}
	if usage.assistantMsg == nil || usage.assistantMsg.Content != "Rudra" || usage.assistantMsg.Role != "assistant" {
		t.Fatalf("reply text not captured: %+v", usage.assistantMsg)
	}
}

func TestWithStreamUsageAddsOption(t *testing.T) {
	raw := []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	var got map[string]any
	if err := json.Unmarshal(withStreamUsage(raw), &got); err != nil {
		t.Fatal(err)
	}
	opts, ok := got["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("stream_options missing: %v", got)
	}
}

type oneByteReader struct{ data []byte }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(o.data) == 0 {
		return 0, io.EOF
	}
	p[0] = o.data[0]
	o.data = o.data[1:]
	return 1, nil
}
