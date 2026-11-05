package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dikshaWagh/meridian/internal/mltui"
	"github.com/dikshaWagh/meridian/internal/tui"
)

func main() {
	fs := flag.NewFlagSet("meridian-tui", flag.ExitOnError)
	addr := fs.String("addr", "http://localhost:8080", "server address")
	maxTokens := fs.Int("max-tokens", 128, "maximum output tokens")
	// ML inference view. The default view is the unchanged LLM chat TUI.
	mlView := fs.Bool("ml", false, "show the ML inference balancer view")
	mlAddr := fs.String("ml-addr", "http://localhost:8090", "ML balancer address")
	mlRefresh := fs.Duration("ml-refresh", time.Second, "ML view refresh interval")
	mlEvents := fs.Int("ml-events", 8, "routing events to show in the ML view")
	fs.Parse(os.Args[1:])

	if *mlView {
		if err := mltui.Dashboard(*mlAddr, *mlRefresh, *mlEvents); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if err := tui.Chat(*addr, *maxTokens); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
