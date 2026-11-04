package tokens

import (
	"github.com/dikshaWagh/meridian/internal/api"
	"testing"
)

func TestText(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want int
	}{{"", 0}, {"abcd", 1}, {"abcde", 2}, {"😀😀😀😀", 1}} {
		if got := Text(tc.s); got != tc.want {
			t.Errorf("Text(%q)=%d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestForRequest(t *testing.T) {
	r := api.ChatRequest{Messages: []api.Message{{Content: "abcd"}, {Content: "abcde"}}}
	e := ForRequest(r)
	if e.Output != DefaultOutputTokens || e.Input != 1+2+2*PerMessageOverhead+ReplyPriming {
		t.Fatalf("unexpected estimate: %+v", e)
	}
	r.MaxTokens = 40
	e = ForRequest(r)
	if e.Output != 40 || e.Total != e.Input+40 {
		t.Fatalf("unexpected bounded estimate: %+v", e)
	}
}
