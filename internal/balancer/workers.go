package balancer

type WorkerSpec struct {
	ID        string
	Model     string
	Port      int
	PrefillMS int // simulated ms per input token
	DecodeMS  int // simulated ms per output token
}

// DefaultWorkers is the hardcoded worker set (static discovery).
func DefaultWorkers() []WorkerSpec {
	return []WorkerSpec{
		{"w1", "qwen-0.5b", 9001, 2, 20},
		{"w2", "qwen-0.5b", 9002, 2, 20},
		{"w3", "smollm-360m", 9003, 1, 12},
		{"w4", "tinyllama-1.1b", 9004, 4, 35},
		{"w5", "tinyllama-1.1b", 9005, 4, 35},
	}
}
