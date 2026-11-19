package main

import "testing"

func TestParseIntsAndList(t *testing.T) {
	got, err := parseInts(" 2, 4 ,8,")
	if err != nil || len(got) != 3 || got[2] != 8 {
		t.Fatalf("parseInts: %v %v", got, err)
	}
	if _, err := parseInts("2,x"); err == nil {
		t.Fatal("expected error for non-integer")
	}
	if l := parseList(" round-robin, ,token-aware"); len(l) != 2 || l[1] != "token-aware" {
		t.Fatalf("parseList: %v", l)
	}
}

func TestConversationIDsDifferPerRun(t *testing.T) {
	models := []string{"a", "b"}
	first := buildConversations(models, 3, "r1")
	second := buildConversations(models, 3, "r2")
	seen := map[string]bool{}
	for _, c := range first {
		seen[c.ConvID] = true
	}
	for _, c := range second {
		if seen[c.ConvID] {
			t.Fatalf("conversation ID %s reused across runs", c.ConvID)
		}
	}
}
