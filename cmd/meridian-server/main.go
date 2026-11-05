package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/ml"
	"github.com/dikshaWagh/meridian/internal/mlworker"
	"github.com/dikshaWagh/meridian/internal/mockworker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		o := balancer.Options{}
		fs.StringVar(&o.Host, "host", "0.0.0.0", "balancer bind address")
		fs.IntVar(&o.Port, "port", 8080, "balancer port")
		fs.StringVar(&o.ModelsDir, "models-dir", "~/models", "GGUF model directory")
		fs.StringVar(&o.LlamaBin, "llama-bin", "llama-server", "llama-server binary")
		fs.IntVar(&o.Threads, "threads", 4, "threads per worker")
		fs.IntVar(&o.Context, "ctx", 2048, "worker context size")
		fs.BoolVar(&o.Mock, "mock", false, "use mock workers")
		fs.IntVar(&o.NGL, "ngl", 0, "GPU layers per worker (0 = CPU only)")
		fs.IntVar(&o.GPUWorkers, "gpu-workers", 1, "number of workers to offload to GPU")
		fs.StringVar(&o.BackendLib, "backend-lib", "", "llama.cpp backend .so (e.g. Vulkan)")
		fs.StringVar(&o.GPUDevice, "gpu-device", "", "device name, e.g. Vulkan1")
		// ML inference subsystem (independent of the LLM balancer above).
		var mlEnabled bool
		var mlSpecs ml.SpecList
		mlOpts := ml.Options{}
		fs.BoolVar(&mlEnabled, "ml", false, "enable the ML inference balancer")
		fs.StringVar(&mlOpts.Listen, "ml-listen", "", "ML balancer bind address (default "+ml.DefaultListen+")")
		fs.StringVar(&mlOpts.ConfigPath, "ml-config", "", "ML config YAML with an ml.workers list")
		fs.Var(&mlSpecs, "ml-worker", "inline ML worker, repeatable: id=resnet-1,model=resnet50,url=http://127.0.0.1:9001")
		fs.Parse(os.Args[2:])
		if mlEnabled || mlOpts.ConfigPath != "" || len(mlSpecs) > 0 {
			mlOpts.Workers = mlSpecs
			startML(mlOpts)
		}
		if err := balancer.Run(o); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "ml-worker":
		fs := flag.NewFlagSet("ml-worker", flag.ExitOnError)
		id := fs.String("id", "ml1", "worker id")
		model := fs.String("model", "resnet50", "model served by this worker")
		port := fs.Int("port", 9001, "port")
		predict := fs.Int("predict-ms", 40, "simulated inference time")
		load := fs.Int("load-ms", 0, "simulated model load time before becoming ready")
		fs.Parse(os.Args[2:])
		if err := mlworker.Run(*id, *model, *port, *predict, *load); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "mock-worker":
		fs := flag.NewFlagSet("mock-worker", flag.ExitOnError)
		id := fs.String("id", "w1", "worker id")
		model := fs.String("model", "qwen2.5-3b", "model")
		port := fs.Int("port", 8081, "port")
		pre := fs.Int("prefill-ms", 2, "prefill delay")
		dec := fs.Int("decode-ms", 20, "decode delay")
		fs.Parse(os.Args[2:])
		if err := mockworker.Run(*id, *model, *port, *pre, *dec); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

// startML runs the ML inference balancer alongside the LLM balancer in the same
// process, on its own listener. A failure here never stops the LLM balancer.
func startML(o ml.Options) {
	s, err := ml.New(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ml: disabled:", err)
		return
	}
	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := s.Serve(ctx); err != nil {
			log.Printf("ml: %v", err)
		}
	}()
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: meridian-server <serve|mock-worker|ml-worker> [flags]")
	os.Exit(2)
}
