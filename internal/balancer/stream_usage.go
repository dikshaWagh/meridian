package balancer

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// withStreamUsage asks llama-server to end a streamed reply with a usage chunk,
// so the gateway learns the real token counts.
func withStreamUsage(raw []byte) []byte {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	m["stream_options"] = map[string]bool{"include_usage": true}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// streamUsageReader passes a server-sent-events body through unchanged while
// reading it. It records the first-token time, the usage chunk, and the reply
// text, filling the same requestUsage the non-streaming path fills.
type streamUsageReader struct {
	src   io.ReadCloser
	usage *requestUsage
	line  []byte
	text  strings.Builder
	done  bool
}

func (s *streamUsageReader) Read(p []byte) (int, error) {
	n, err := s.src.Read(p)
	s.scan(p[:n])
	if err == io.EOF {
		s.finish()
	}
	return n, err
}

func (s *streamUsageReader) Close() error { return s.src.Close() }

func (s *streamUsageReader) scan(chunk []byte) {
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			s.line = append(s.line, chunk...)
			return
		}
		s.line = append(s.line, chunk[:i]...)
		s.handleLine(s.line)
		s.line = s.line[:0]
		chunk = chunk[i+1:]
	}
}

func (s *streamUsageReader) handleLine(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	data := bytes.TrimSpace(line[len("data:"):])
	if len(data) == 0 || string(data) == "[DONE]" {
		return
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *api.Usage `json:"usage"`
	}
	if json.Unmarshal(data, &chunk) != nil {
		return
	}
	for _, c := range chunk.Choices {
		if c.Delta.Content == "" {
			continue
		}
		if s.usage.firstTok.IsZero() {
			s.usage.firstTok = time.Now()
		}
		s.text.WriteString(c.Delta.Content)
	}
	if chunk.Usage != nil {
		s.usage.in = chunk.Usage.PromptTokens
		s.usage.out = chunk.Usage.CompletionTokens
		s.usage.ok = true
	}
}

// finish runs once the stream ends. It reads a final line that had no newline
// and stores the reply text for the conversation history.
func (s *streamUsageReader) finish() {
	if s.done {
		return
	}
	s.done = true
	if len(s.line) > 0 {
		s.handleLine(s.line)
		s.line = nil
	}
	if s.text.Len() > 0 {
		s.usage.assistantMsg = &api.Message{Role: "assistant", Content: s.text.String()}
	}
}
