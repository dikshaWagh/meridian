package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/ml"
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
		fs.IntVar(&o.Context, "ctx", 8192, "worker context size")
		fs.IntVar(&o.Parallel, "parallel", 2, "parallel slots per worker")
		fs.BoolVar(&o.Mock, "mock", false, "use mock workers")
		fs.IntVar(&o.NGL, "ngl", 0, "GPU layers per worker (0 = CPU only)")
		fs.IntVar(&o.GPUWorkers, "gpu-workers", 1, "number of workers to offload to GPU")
		fs.StringVar(&o.BackendLib, "backend-lib", "", "llama.cpp backend .so (e.g. Vulkan)")
		fs.StringVar(&o.GPUDevice, "gpu-device", "", "device name, e.g. Vulkan1")
		fs.StringVar(&o.RoutingPolicy, "routing-policy", "round-robin", "default routing policy (see /policies)")
		fs.BoolVar(&o.EnableLLM, "enable-llm", true, "enable the LLM serving subsystem")
		fs.BoolVar(&o.EnableML, "enable-ml", false, "enable the ML (OCR/image generation) serving subsystem")
		fs.StringVar(&o.MLConfig, "ml-config", "configs/ml.yaml", "path to the ML YAML configuration")
		fs.Parse(os.Args[2:])
		if err := balancer.Run(o); err != nil {
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
	case "ml-worker":
		fs := flag.NewFlagSet("ml-worker", flag.ExitOnError)
		kind := fs.String("kind", "ocr", "worker kind: ocr or image-generation")
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
	fmt.Fprintln(os.Stderr, "usage: meridian-server <serve|mock-worker|ml-worker> [flags]")
	os.Exit(2)
}
