package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/cli"
	"github.com/dikshaWagh/meridian/internal/mockworker"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: meridian <command> [flags]

commands:
  serve    start the load balancer and its mock workers
  worker   run a single mock worker (normally started by serve)
  chat     interactive chat client`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		port := fs.Int("port", 8080, "balancer port")
		fs.Parse(args)
		err = balancer.Run(*port)
	case "worker":
		fs := flag.NewFlagSet("worker", flag.ExitOnError)
		id := fs.String("id", "w1", "worker id")
		model := fs.String("model", "qwen-0.5b", "model name")
		port := fs.Int("port", 9001, "listen port")
		prefill := fs.Int("prefill-ms", 2, "ms per input token")
		decode := fs.Int("decode-ms", 20, "ms per output token")
		fs.Parse(args)
		err = mockworker.Run(*id, *model, *port, *prefill, *decode)
	case "chat":
		fs := flag.NewFlagSet("chat", flag.ExitOnError)
		addr := fs.String("addr", "http://localhost:8080", "balancer address")
		fs.Parse(args)
		err = cli.Chat(*addr)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
