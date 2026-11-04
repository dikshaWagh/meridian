package tokens

import (
	"unicode/utf8"

	"github.com/dikshaWagh/meridian/internal/api"
)

const (
	CharsPerToken       = 4
	PerMessageOverhead  = 4
	ReplyPriming        = 3
	DefaultOutputTokens = 128
)

type Estimate struct{ Input, Output, Total int }

// Text is a deliberately rough, model-independent estimate.
func Text(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + CharsPerToken - 1) / CharsPerToken
}

func ForRequest(req api.ChatRequest) Estimate {
	in := ReplyPriming
	for _, m := range req.Messages {
		in += Text(m.Content) + PerMessageOverhead
	}
	out := req.MaxTokens
	if out <= 0 {
		out = DefaultOutputTokens
	}
	return Estimate{Input: in, Output: out, Total: in + out}
}
