package balancer

import (
	"testing"

	"github.com/dikshaWagh/meridian/internal/api"
)

// TestIntegration_FullMultiModelFlow simulates the complete flow from Options
// through to worker pool creation.
func TestIntegration_FullMultiModelFlow(t *testing.T) {
	// Simulate what the CLI parser creates
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "qwen.gguf", Workers: 2},
			{Model: "llama.gguf", Workers: 3},
			{Model: "mistral.gguf", Workers: 1},
		},
		ModelsDir: "/models",
	}

	// Generate worker specs
	specs := workerSpecs(o)

	// Verify specs
	if len(specs) != 6 {
		t.Fatalf("expected 6 workers, got %d", len(specs))
	}

	// Verify models
	expectedModels := []string{"qwen", "qwen", "llama", "llama", "llama", "mistral"}
	for i, expected := range expectedModels {
		if specs[i].Model != expected {
			t.Fatalf("specs[%d].Model: expected %s, got %s", i, expected, specs[i].Model)
		}
	}

	// Verify ports are sequential
	for i := 0; i < 6; i++ {
		expectedPort := 8081 + i
		if specs[i].Port != expectedPort {
			t.Fatalf("specs[%d].Port: expected %d, got %d", i, expectedPort, specs[i].Port)
		}
	}

	// Verify files
	expectedFiles := []string{
		"qwen.gguf", "qwen.gguf",
		"llama.gguf", "llama.gguf", "llama.gguf",
		"mistral.gguf",
	}
	for i, expected := range expectedFiles {
		if specs[i].File != expected {
			t.Fatalf("specs[%d].File: expected %s, got %s", i, expected, specs[i].File)
		}
	}

	// Now create a pool and verify routing works
	pool := NewPool(specs)

	// Mark all workers as healthy
	for _, w := range pool.Workers() {
		w.setHealthy(true)
	}

	// Verify each model can be picked
	models := []string{"qwen", "llama", "mistral"}
	for _, model := range models {
		w, err := pool.Pick(model)
		if err != nil {
			t.Fatalf("failed to pick model %s: %v", model, err)
		}
		if w.Spec.Model != model {
			t.Fatalf("picked worker with model %s, expected %s", w.Spec.Model, model)
		}
	}

	// Verify unknown model fails
	_, err := pool.Pick("unknown")
	if err != ErrModelNotFound {
		t.Fatalf("expected ErrModelNotFound for unknown model, got %v", err)
	}

	// Verify model catalog
	catalog := pool.ModelCatalog()
	if len(catalog) != 3 {
		t.Fatalf("expected 3 models in catalog, got %d", len(catalog))
	}

	// Count workers per model in catalog
	modelCounts := make(map[string]int)
	for _, m := range catalog {
		modelCounts[m.ID] = m.Workers
	}

	if modelCounts["qwen"] != 2 {
		t.Fatalf("qwen should have 2 workers, got %d", modelCounts["qwen"])
	}
	if modelCounts["llama"] != 3 {
		t.Fatalf("llama should have 3 workers, got %d", modelCounts["llama"])
	}
	if modelCounts["mistral"] != 1 {
		t.Fatalf("mistral should have 1 worker, got %d", modelCounts["mistral"])
	}
}

// TestIntegration_BackwardsCompatibility verifies that old Options struct
// is properly converted to new LLMConfigs format.
func TestIntegration_BackwardsCompatibility(t *testing.T) {
	// Old-style options
	o := Options{
		LLMModel:   "qwen.gguf",
		LLMWorkers: 2,
	}

	// Simulate Run() converting old to new format
	if o.LLMModel != "" && len(o.LLMConfigs) == 0 {
		if o.LLMWorkers < 1 {
			o.LLMWorkers = 1
		}
		o.LLMConfigs = []LLMConfig{{Model: o.LLMModel, Workers: o.LLMWorkers}}
	}

	// Now it should work like new format
	specs := workerSpecs(o)
	if len(specs) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(specs))
	}

	for _, s := range specs {
		if s.Model != "qwen" {
			t.Fatalf("expected model qwen, got %s", s.Model)
		}
	}
}

// TestIntegration_MixedAbsoluteRelativePaths tests path handling
func TestIntegration_MixedAbsoluteRelativePaths(t *testing.T) {
	o := Options{
		LLMConfigs: []LLMConfig{
			{Model: "/data/models/qwen.gguf", Workers: 1},
			{Model: "llama.gguf", Workers: 1},
			{Model: "../shared/mistral.gguf", Workers: 1},
		},
	}

	specs := workerSpecs(o)
	if len(specs) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(specs))
	}

	// Verify model names extracted from paths
	if specs[0].Model != "qwen" {
		t.Fatalf("expected qwen from /data/models/qwen.gguf, got %s", specs[0].Model)
	}
	if specs[1].Model != "llama" {
		t.Fatalf("expected llama from llama.gguf, got %s", specs[1].Model)
	}
	if specs[2].Model != "mistral" {
		t.Fatalf("expected mistral from ../shared/mistral.gguf, got %s", specs[2].Model)
	}

	// Verify files preserved
	if specs[0].File != "/data/models/qwen.gguf" {
		t.Fatalf("expected /data/models/qwen.gguf, got %s", specs[0].File)
	}
	if specs[1].File != "llama.gguf" {
		t.Fatalf("expected llama.gguf, got %s", specs[1].File)
	}
	if specs[2].File != "../shared/mistral.gguf" {
		t.Fatalf("expected ../shared/mistral.gguf, got %s", specs[2].File)
	}
}

// TestIntegration_PickingWorkersCrossModel verifies that picking workers
// correctly filters by model and doesn't cross-contaminate
func TestIntegration_PickingWorkersCrossModel(t *testing.T) {
	specs := []WorkerSpec{
		{ID: "w1", Model: "qwen", Port: 8081, File: "qwen.gguf"},
		{ID: "w2", Model: "qwen", Port: 8082, File: "qwen.gguf"},
		{ID: "w3", Model: "llama", Port: 8083, File: "llama.gguf"},
		{ID: "w4", Model: "llama", Port: 8084, File: "llama.gguf"},
		{ID: "w5", Model: "mistral", Port: 8085, File: "mistral.gguf"},
	}
	pool := NewPool(specs)

	// Make only w1 unhealthy
	pool.Workers()[0].setHealthy(false)
	pool.Workers()[1].setHealthy(true)
	pool.Workers()[2].setHealthy(true)
	pool.Workers()[3].setHealthy(true)
	pool.Workers()[4].setHealthy(true)

	// Picking qwen should get w2 (w1 is unhealthy)
	w, err := pool.Pick("qwen")
	if err != nil {
		t.Fatalf("failed to pick qwen: %v", err)
	}
	if w.Spec.ID != "w2" {
		t.Fatalf("expected w2, got %s", w.Spec.ID)
	}

	// Picking llama should get one of w3 or w4 (round-robin)
	w, err = pool.Pick("llama")
	if err != nil {
		t.Fatalf("failed to pick llama: %v", err)
	}
	if w.Spec.Model != "llama" {
		t.Fatalf("expected llama, got %s", w.Spec.Model)
	}

	// Verify models are isolated (picking llama never returns qwen)
	for i := 0; i < 10; i++ {
		w, err := pool.Pick("llama")
		if err != nil {
			t.Fatalf("iteration %d: failed to pick llama: %v", i, err)
		}
		if w.Spec.Model != "llama" {
			t.Fatalf("iteration %d: expected llama, got %s", i, w.Spec.Model)
		}
	}
}

// TestIntegration_ModelCatalogWithHealthStatus verifies catalog includes
// correct health information per model
func TestIntegration_ModelCatalogWithHealthStatus(t *testing.T) {
	specs := []WorkerSpec{
		{ID: "w1", Model: "qwen", Port: 8081, File: "qwen.gguf"},
		{ID: "w2", Model: "qwen", Port: 8082, File: "qwen.gguf"},
		{ID: "w3", Model: "llama", Port: 8083, File: "llama.gguf"},
		{ID: "w4", Model: "llama", Port: 8084, File: "llama.gguf"},
		{ID: "w5", Model: "mistral", Port: 8085, File: "mistral.gguf"},
	}
	pool := NewPool(specs)

	// Set different health states
	pool.Workers()[0].setHealthy(true)  // w1 qwen: healthy
	pool.Workers()[1].setHealthy(false) // w2 qwen: unhealthy
	pool.Workers()[2].setHealthy(true)  // w3 llama: healthy
	pool.Workers()[3].setHealthy(true)  // w4 llama: healthy
	// w5 mistral: still starting

	catalog := pool.ModelCatalog()

	// Find each model in catalog
	modelCatalog := make(map[string]*api.CatalogModel)
	for i, m := range catalog {
		modelCatalog[m.ID] = &catalog[i]
	}

	// qwen: 2 total, 1 healthy, 0 starting
	if m, ok := modelCatalog["qwen"]; ok {
		if m.Workers != 2 || m.HealthyWorkers != 1 {
			t.Fatalf("qwen: expected 2 total 1 healthy, got %d total %d healthy",
				m.Workers, m.HealthyWorkers)
		}
	} else {
		t.Fatalf("qwen not in catalog")
	}

	// llama: 2 total, 2 healthy, 0 starting
	if m, ok := modelCatalog["llama"]; ok {
		if m.Workers != 2 || m.HealthyWorkers != 2 {
			t.Fatalf("llama: expected 2 total 2 healthy, got %d total %d healthy",
				m.Workers, m.HealthyWorkers)
		}
	} else {
		t.Fatalf("llama not in catalog")
	}

	// mistral: 1 total, 0 healthy, 1 starting
	if m, ok := modelCatalog["mistral"]; ok {
		if m.Workers != 1 {
			t.Fatalf("mistral: expected 1 total, got %d", m.Workers)
		}
		if m.State != "starting" {
			t.Fatalf("mistral: expected starting state, got %s", m.State)
		}
	} else {
		t.Fatalf("mistral not in catalog")
	}
}
