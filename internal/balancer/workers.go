package balancer

import "fmt"

type WorkerSpec struct {
	ID    string
	Model string
	Port  int
	File  string
}

// DefaultWorkers is the hardcoded worker set (static discovery).
func DefaultWorkers() []WorkerSpec {
	return []WorkerSpec{
		{"w1", "qwen2.5-3b", 8081, "qwen2.5-3b-instruct-q4_k_m.gguf"},
		{"w2", "qwen2.5-3b", 8082, "qwen2.5-3b-instruct-q4_k_m.gguf"},
		{"w3", "llama-3.2-3b", 8083, "llama-3.2-3b-instruct-q4_k_m.gguf"},
		{"w4", "llama-3.2-3b", 8084, "llama-3.2-3b-instruct-q4_k_m.gguf"},
		{"w5", "qwen2.5-0.5b", 8085, "qwen2.5-0.5b-instruct-q4_k_m.gguf"},
		{"w6", "qwen2.5-0.5b", 8086, "qwen2.5-0.5b-instruct-q4_k_m.gguf"},
	}
}

// SingleModelWorkers serves one GGUF file with n identical workers on
// consecutive ports starting at 8081.
func SingleModelWorkers(file, name string, n int) []WorkerSpec {
	if n < 1 {
		n = 1
	}
	if name == "" {
		name = file
	}
	out := make([]WorkerSpec, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, WorkerSpec{
			ID:    fmt.Sprintf("w%d", i+1),
			Model: name,
			Port:  8081 + i,
			File:  file,
		})
	}
	return out
}
