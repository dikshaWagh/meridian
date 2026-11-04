package balancer

type WorkerSpec struct {
	ID        string
	Model     string
	Port      int
	File      string
	PrefillMS int // simulated ms per input token
	DecodeMS  int // simulated ms per output token
}

// DefaultWorkers is the hardcoded worker set (static discovery).
func DefaultWorkers() []WorkerSpec {
	return []WorkerSpec{
		{"w1", "qwen2.5-3b", 8081, "qwen2.5-3b-instruct-q4_k_m.gguf", 2, 20},
		{"w2", "qwen2.5-3b", 8082, "qwen2.5-3b-instruct-q4_k_m.gguf", 2, 20},
		{"w3", "llama-3.2-3b", 8083, "llama-3.2-3b-instruct-q4_k_m.gguf", 1, 12},
		{"w4", "llama-3.2-3b", 8084, "llama-3.2-3b-instruct-q4_k_m.gguf", 1, 12},
	}
}
