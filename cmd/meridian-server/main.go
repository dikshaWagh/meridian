package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/cluster"
	"github.com/dikshaWagh/meridian/internal/config"
	"github.com/dikshaWagh/meridian/internal/ml"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "version", "--version", "-version":
		printVersion()
		return
	case "serve":
		cfg, err := config.Parse(os.Args[2:], os.LookupEnv)
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		if cfg.Role != config.RoleStandalone {
			if err := runCluster(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		}
		if err := balancer.Run(cfg.Options); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "ml-worker":
		fs := flag.NewFlagSet("ml-worker", flag.ExitOnError)
		kind := fs.String("kind", "ocr", "worker kind (ocr)")
		config := fs.String("config", "", "path to the worker JSON config")
		listen := fs.String("listen", "127.0.0.1:8091", "loopback listen address")
		fs.Parse(os.Args[2:])
		if err := ml.RunWorker(*kind, *config, *listen); err != nil {
			fmt.Fprintln(os.Stderr, "ml-worker error:", err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: meridian-server <serve|ml-worker|version> [flags]")
	os.Exit(2)
}

// runCluster checks this machine, logs the resolved settings and runs the role.
func runCluster(cfg *config.ServeConfig) error {
	if err := cfg.Prepare(); err != nil {
		return err
	}
	for _, line := range cfg.Describe() {
		log.Println(line)
	}
	if cfg.Role == config.RoleWorker {
		return cluster.RunWorker(cfg)
	}
	return cluster.RunLeader(cfg)
}

// printVersion reports which executable is actually running and how it was
// built. Use it to rule out a stale binary before debugging anything else.
func printVersion() {
	exe, _ := os.Executable()
	fmt.Printf("executable: %s\n", exe)
	if fi, err := os.Stat(exe); err == nil {
		fmt.Printf("modified:   %s\n", fi.ModTime().Format("2006-01-02 15:04:05"))
		fmt.Printf("size:       %d bytes\n", fi.Size())
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("build:      (unknown)")
		return
	}
	rev, at, dirty := "", "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		rev = "(no vcs)"
	}
	if dirty {
		rev += "+dirty"
	}
	fmt.Printf("build:      %s %s (%s)\n", rev, at, bi.GoVersion)
}
