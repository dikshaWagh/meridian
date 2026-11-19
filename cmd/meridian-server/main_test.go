package main

import "testing"

func TestLLMFlagParsing(t *testing.T) {
	f := llmFlagSlice{"qwen.gguf:2", "/m/llama-3b.gguf", "mistral.gguf:1"}
	cfgs, err := f.toConfigs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []struct {
		model   string
		workers int
	}{{"qwen.gguf", 2}, {"/m/llama-3b.gguf", 1}, {"mistral.gguf", 1}}
	if len(cfgs) != len(want) {
		t.Fatalf("expected %d configs, got %d", len(want), len(cfgs))
	}
	for i, w := range want {
		if cfgs[i].Model != w.model || cfgs[i].Workers != w.workers {
			t.Fatalf("config %d: got %+v, want %+v", i, cfgs[i], w)
		}
	}
}

func TestLLMFlagParsingRejectsBadCounts(t *testing.T) {
	for _, v := range []string{"qwen.gguf:0", "qwen.gguf:abc", "qwen.gguf:-1", ":2", "  "} {
		if _, err := (llmFlagSlice{v}).toConfigs(); err == nil {
			t.Fatalf("expected error for %q", v)
		}
	}
}
