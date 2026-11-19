package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/dikshaWagh/meridian/internal/balancer"
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
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		o := balancer.Options{}
		fs.StringVar(&o.Host, "host", "0.0.0.0", "balancer bind address")
		fs.IntVar(&o.Port, "port", 8080, "balancer port")
		fs.StringVar(&o.ModelsDir, "models-dir", "~/models", "GGUF model directory")
		fs.StringVar(&o.LlamaBin, "llama-bin", "llama-server", "llama-server binary")
		fs.IntVar(&o.Threads, "threads", 4, "threads per worker")
		fs.IntVar(&o.Context, "ctx", 8192, "worker context size")
		fs.IntVar(&o.Parallel, "parallel", 2, "parallel slots per worker")
		fs.IntVar(&o.NGL, "ngl", 0, "GPU layers per worker (0 = CPU only)")
		fs.IntVar(&o.GPUWorkers, "gpu-workers", 1, "number of workers to offload to GPU")
		fs.StringVar(&o.BackendLib, "backend-lib", "", "llama.cpp backend .so (e.g. Vulkan)")
		fs.StringVar(&o.GPUDevice, "gpu-device", "", "device name, e.g. Vulkan1")
		fs.StringVar(&o.RoutingPolicy, "routing-policy", "round-robin", "default routing policy (see /policies)")
		fs.BoolVar(&o.EnableLLM, "enable-llm", true, "enable the LLM serving subsystem")
		fs.BoolVar(&o.EnableML, "enable-ml", false, "enable the OCR serving subsystem")
		fs.StringVar(&o.LLMModel, "llm-model", "", "serve one GGUF model file instead of the built-in worker set (relative paths use --models-dir)")
		fs.StringVar(&o.LLMModelName, "llm-model-name", "", "public model id for --llm-model (defaults to the file name)")
		fs.IntVar(&o.LLMWorkers, "llm-workers", 1, "number of workers for --llm-model")
		fs.StringVar(&o.MLConfig, "ml-config", "configs/ml.yaml", "path to the ML YAML configuration")
		fs.Parse(os.Args[2:])
		if err := balancer.Run(o); err != nil {
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
