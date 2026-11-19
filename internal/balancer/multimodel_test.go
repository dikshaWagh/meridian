package balancer

import (
	"testing"
)

func TestWorkerSpecsWithEmptyConfigIsEmpty(t *testing.T) {
	o := Options{LLMConfigs: []LLMConfig{}}
	if specs := workerSpecs(o); len(specs) != 0 {
		t.Fatalf("expected no workers without --llm, got %d", len(specs))
	}
}

func TestValidateLLMConfigs(t *testing.T) {
	if err := validateLLMConfigs(nil); err == nil {
		t.Fatal("expected error for no models")
	}
	if err := validateLLMConfigs([]LLMConfig{{Model: "a.gguf", Workers: 1}, {Model: "/x/a.gguf", Workers: 2}}); err == nil {
		t.Fatal("expected error for the same model name listed twice")
	}
	if err := validateLLMConfigs([]LLMConfig{{Model: "a.gguf", Workers: 1}, {Model: "b.gguf", Workers: 3}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerSpecsWithSingleModel(t *testing.T) {
	// Single model config with 2 workers
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "qwen.gguf", Workers: 2},
		},
	}
	specs := workerSpecs(o)
	if len(specs) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(specs))
	}
	if specs[0].Model != "qwen" {
		t.Fatalf("expected model name 'qwen', got %s", specs[0].Model)
	}
	if specs[0].Port != 8081 || specs[1].Port != 8082 {
		t.Fatalf("expected ports 8081, 8082, got %d, %d", specs[0].Port, specs[1].Port)
	}
	if specs[0].File != "qwen.gguf" {
		t.Fatalf("expected file 'qwen.gguf', got %s", specs[0].File)
	}
}

func TestWorkerSpecsWithMultipleModels(t *testing.T) {
	// Multiple models with different worker counts
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "qwen.gguf", Workers: 2},
			{Model: "llama.gguf", Workers: 3},
			{Model: "mistral.gguf", Workers: 1},
		},
	}
	specs := workerSpecs(o)
	if len(specs) != 6 {
		t.Fatalf("expected 6 total workers (2+3+1), got %d", len(specs))
	}

	// Check qwen workers
	for i := 0; i < 2; i++ {
		if specs[i].Model != "qwen" {
			t.Fatalf("expected specs[%d].Model to be 'qwen', got %s", i, specs[i].Model)
		}
	}

	// Check llama workers
	for i := 2; i < 5; i++ {
		if specs[i].Model != "llama" {
			t.Fatalf("expected specs[%d].Model to be 'llama', got %s", i, specs[i].Model)
		}
	}

	// Check mistral worker
	if specs[5].Model != "mistral" {
		t.Fatalf("expected specs[5].Model to be 'mistral', got %s", specs[5].Model)
	}

	// Check ports are sequential
	for i := 0; i < 6; i++ {
		if specs[i].Port != 8081+i {
			t.Fatalf("expected specs[%d].Port to be %d, got %d", i, 8081+i, specs[i].Port)
		}
	}
}

func TestWorkerSpecsWithDefaultWorkerCount(t *testing.T) {
	// Model without explicit worker count should default to 1
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "qwen.gguf", Workers: 0}, // 0 should become 1
		},
	}
	specs := workerSpecs(o)
	if len(specs) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(specs))
	}
}

func TestWorkerSpecsWithPathHandling(t *testing.T) {
	// Test with full paths
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "/models/qwen.gguf", Workers: 1},
			{Model: "llama-3b-instruct-q4.gguf", Workers: 1},
		},
	}
	specs := workerSpecs(o)
	if len(specs) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(specs))
	}

	// Full path should extract basename
	if specs[0].Model != "qwen" {
		t.Fatalf("expected model 'qwen' from /models/qwen.gguf, got %s", specs[0].Model)
	}
	if specs[0].File != "/models/qwen.gguf" {
		t.Fatalf("expected file '/models/qwen.gguf', got %s", specs[0].File)
	}

	// Relative path
	if specs[1].Model != "llama-3b-instruct-q4" {
		t.Fatalf("expected model 'llama-3b-instruct-q4', got %s", specs[1].Model)
	}
	if specs[1].File != "llama-3b-instruct-q4.gguf" {
		t.Fatalf("expected file 'llama-3b-instruct-q4.gguf', got %s", specs[1].File)
	}
}

func TestWorkerIDs(t *testing.T) {
	// Worker IDs should be unique and sequential
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "qwen.gguf", Workers: 2},
			{Model: "llama.gguf", Workers: 3},
		},
	}
	specs := workerSpecs(o)

	ids := make(map[string]bool)
	for i, spec := range specs {
		if ids[spec.ID] {
			t.Fatalf("duplicate worker ID: %s", spec.ID)
		}
		ids[spec.ID] = true

		// IDs should be like w1, w2, ..., w5
		expectedID := "w" + string(rune('1'+i))
		if spec.ID != expectedID {
			t.Fatalf("expected worker ID %s at index %d, got %s", expectedID, i, spec.ID)
		}
	}
}

func TestPoolCanPickFromMultipleModels(t *testing.T) {
	// Create a pool with multiple models
	specs := []WorkerSpec{
		{ID: "w1", Model: "qwen", Port: 8081, File: "qwen.gguf"},
		{ID: "w2", Model: "qwen", Port: 8082, File: "qwen.gguf"},
		{ID: "w3", Model: "llama", Port: 8083, File: "llama.gguf"},
		{ID: "w4", Model: "llama", Port: 8084, File: "llama.gguf"},
	}
	pool := NewPool(specs)

	// Mark all workers as healthy for testing
	for _, w := range pool.Workers() {
		w.setHealthy(true)
	}

	// Should be able to pick from qwen model
	w, err := pool.Pick("qwen")
	if err != nil {
		t.Fatalf("failed to pick from qwen: %v", err)
	}
	if w.Spec.Model != "qwen" {
		t.Fatalf("expected qwen model, got %s", w.Spec.Model)
	}

	// Should be able to pick from llama model
	w, err = pool.Pick("llama")
	if err != nil {
		t.Fatalf("failed to pick from llama: %v", err)
	}
	if w.Spec.Model != "llama" {
		t.Fatalf("expected llama model, got %s", w.Spec.Model)
	}

	// Should fail for unknown model
	_, err = pool.Pick("unknown")
	if err != ErrModelNotFound {
		t.Fatalf("expected ErrModelNotFound, got %v", err)
	}
}

func TestModelCatalogWithMultipleModels(t *testing.T) {
	// Create a pool with multiple models
	specs := []WorkerSpec{
		{ID: "w1", Model: "qwen", Port: 8081, File: "qwen.gguf"},
		{ID: "w2", Model: "qwen", Port: 8082, File: "qwen.gguf"},
		{ID: "w3", Model: "llama", Port: 8083, File: "llama.gguf"},
		{ID: "w4", Model: "mistral", Port: 8084, File: "mistral.gguf"},
	}
	pool := NewPool(specs)

	// Mark some workers as healthy
	pool.Workers()[0].setHealthy(true)
	pool.Workers()[1].setHealthy(true)
	pool.Workers()[2].setHealthy(true)

	catalog := pool.ModelCatalog()
	if len(catalog) != 3 {
		t.Fatalf("expected 3 models in catalog, got %d", len(catalog))
	}

	// Check catalog contains all models
	modelMap := make(map[string]bool)
	for _, m := range catalog {
		modelMap[m.ID] = true
	}
	if !modelMap["qwen"] || !modelMap["llama"] || !modelMap["mistral"] {
		t.Fatalf("catalog missing expected models: %v", catalog)
	}

	// Check qwen has 2 workers, 2 healthy
	for _, m := range catalog {
		if m.ID == "qwen" {
			if m.Workers != 2 || m.HealthyWorkers != 2 {
				t.Fatalf("qwen: expected 2 workers 2 healthy, got %d workers %d healthy", m.Workers, m.HealthyWorkers)
			}
		}
	}
}
