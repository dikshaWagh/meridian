package main

import (
	"flag"
	"fmt"
	"github.com/dikshaWagh/meridian/internal/tui"
	"os"
)

func main() {
	fs := flag.NewFlagSet("meridian-tui", flag.ExitOnError)
	addr := fs.String("addr", "http://localhost:8080", "server address")
	maxTokens := fs.Int("max-tokens", 128, "maximum output tokens")
	fs.Parse(os.Args[1:])
	if err := tui.Chat(*addr, *maxTokens); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
