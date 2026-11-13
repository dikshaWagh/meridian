package balancer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dikshaWagh/meridian/internal/api"
)

func apiTestPool() *Pool {
	return NewPool([]WorkerSpec{
		{ID: "w1", Model: "qwen2.5-3b", Port: 18081},
		{ID: "w2", Model: "qwen2.5-3b", Port: 18082},
		{ID: "w3", Model: "llama-3.2-3b", Port: 18083},
	})
}

func TestSetDefaultRoutingPolicyEndpoint(t *testing.T) {
	pool := apiTestPool()
	h := NewHandler(pool)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/policies/default",
		strings.NewReader(`{"name":"least-connections"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := pool.DefaultRoutingPolicy(); got != "least-connections" {
		t.Fatalf("default policy = %q", got)
	}
	var list api.RoutingPolicyList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.Default != "least-connections" || len(list.Policies) != 3 {
		t.Fatalf("unexpected body: %+v", list)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/policies/default",
		strings.NewReader(`{"name":"does-not-exist"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown policy: status = %d", rec.Code)
	}
	if got := pool.DefaultRoutingPolicy(); got != "least-connections" {
		t.Fatalf("default policy changed on error: %q", got)
	}
}

func TestCatalogListsStoppedLLMModels(t *testing.T) {
	pool := apiTestPool()
	h := handleCatalog(pool, nil, nil, ServingExclusive)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out api.CatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.LLM) != 2 {
		t.Fatalf("llm models = %d, want 2", len(out.LLM))
	}
	for _, m := range out.LLM {
		if m.Kind != "llm" {
			t.Fatalf("kind = %q", m.Kind)
		}
		if m.Workers != 2 && m.ID == "qwen2.5-3b" {
			t.Fatalf("workers = %d, want 2", m.Workers)
		}
		if m.State != "starting" {
			// No health probe has run yet: every worker is still starting.
			t.Fatalf("state = %q, want starting", m.State)
		}
	}
	if len(out.RoutingPolicies) != 3 || out.DefaultRoutingPolicy == "" {
		t.Fatalf("policies missing: %+v", out)
	}
}
