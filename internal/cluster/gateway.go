package cluster

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/control"
	"github.com/dikshaWagh/meridian/internal/ml"
	"github.com/dikshaWagh/meridian/internal/registry"
	"github.com/dikshaWagh/meridian/internal/tokens"
)

const (
	maxChatBody = 1 << 20
	// maxAttempts is the first try plus one retry on another replica. The
	// retry happens only before any response bytes reach the client.
	maxAttempts = 2
	// reserveRetries bounds how often a pick is redone when a replica fills up
	// between the eligibility check and the reservation.
	reserveRetries = 5
)

// gateway is the leader's HTTP API. It chooses a replica for every request
// from the registry and forwards it. Workers never choose.
type gateway struct {
	reg      registry.Registry
	router   *balancer.ClusterRouter
	secret   []byte
	leaderID string
	now      func() time.Time
}

func newGateway(reg registry.Registry, router *balancer.ClusterRouter, secret []byte, leaderID string) *gateway {
	return &gateway{reg: reg, router: router, secret: secret, leaderID: leaderID, now: time.Now}
}

// handler returns the leader's API. The caller adds CORS and the UI.
func (g *gateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, statusOf(g.reg, g.leaderID))
	})
	mux.HandleFunc("GET /status", g.handleSystemStatus)
	mux.HandleFunc("GET /v1/models", g.handleModels)
	mux.HandleFunc("GET /v1/catalog", g.handleCatalog)
	mux.HandleFunc("GET /policies", g.handlePolicies)
	mux.HandleFunc("PUT /v1/policies/default", g.handleSetDefault)
	mux.HandleFunc("POST /v1/chat/completions", g.handleChat)
	// The cluster does not run OCR yet, so the ML endpoints answer "disabled"
	// instead of 404.
	ml.RegisterFunc(mux, func() *ml.Service { return nil })
	return mux
}

type routeError struct {
	status int
	typ    string
	msg    string
}

func (g *gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatBody))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "could not read body (max 1 MiB)")
		return
	}
	var req api.ChatRequest
	if json.Unmarshal(raw, &req) != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "body is not valid JSON")
		return
	}
	if req.Model == "" {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	policy, err := g.router.Resolve(strings.TrimSpace(r.Header.Get("X-Meridian-Routing-Policy")))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "unknown_routing_policy", err.Error())
		return
	}
	if len(g.reg.ReplicasForModel(req.Model)) == 0 {
		api.WriteError(w, http.StatusNotFound, "model_not_found", "unknown model "+strconv.Quote(req.Model))
		return
	}

	est := tokens.ForRequest(req)
	route := balancer.RouteRequest{InputTokens: est.Input, OutputTokens: est.Output}
	tried := map[string]bool{}
	failover := ""
	for attempt := 0; attempt < maxAttempts; attempt++ {
		rep, rerr := g.choose(req.Model, policy, route, tried)
		if rerr != nil {
			if attempt == 0 {
				api.WriteError(w, rerr.status, rerr.typ, rerr.msg)
			} else {
				api.WriteError(w, http.StatusBadGateway, "upstream_error", "no other replica could serve the request")
			}
			return
		}
		if g.forward(w, r, rep, raw, policy, failover) {
			g.reg.Release(rep.ReplicaID, true)
			return
		}
		g.reg.Release(rep.ReplicaID, false)
		tried[rep.ReplicaID] = true
		failover = rep.ReplicaID
	}
	api.WriteError(w, http.StatusBadGateway, "upstream_error", "every attempt to reach a replica failed")
}

// choose selects a replica and reserves a slot on it. The caller releases the
// slot when the request ends.
func (g *gateway) choose(model, policy string, route balancer.RouteRequest, skip map[string]bool) (registry.ReplicaRecord, *routeError) {
	for try := 0; try < reserveRetries; try++ {
		var eligible []registry.ReplicaRecord
		for _, r := range g.reg.EligibleReplicas(model) {
			if !skip[r.ReplicaID] {
				eligible = append(eligible, r)
			}
		}
		if len(eligible) == 0 {
			return registry.ReplicaRecord{}, &routeError{http.StatusServiceUnavailable, "no_healthy_worker",
				"no healthy replica for model " + strconv.Quote(model)}
		}

		// A replica's context is split across its parallel slots, and a
		// request must fit in one slot.
		var fit []registry.ReplicaRecord
		for _, r := range eligible {
			if slot := slotContext(r); slot <= 0 || route.InputTokens+route.OutputTokens <= slot {
				fit = append(fit, r)
			}
		}
		if len(fit) == 0 {
			return registry.ReplicaRecord{}, &routeError{http.StatusRequestEntityTooLarge, "context_too_long",
				"prompt exceeds the context window of every healthy replica for this model"}
		}

		cands := make([]balancer.Candidate, len(fit))
		for i, r := range fit {
			cands[i] = balancer.Candidate{ID: r.ReplicaID, Load: r.InFlight, TokPerSec: r.TokPerSec}
		}
		idx, err := g.router.Pick(policy, model, cands, route)
		if err != nil {
			return registry.ReplicaRecord{}, &routeError{http.StatusBadRequest, "unknown_routing_policy", err.Error()}
		}
		rep := fit[idx]
		if g.reg.Reserve(rep.ReplicaID) {
			return rep, nil
		}
	}
	return registry.ReplicaRecord{}, &routeError{http.StatusServiceUnavailable, "no_healthy_worker",
		"replicas for model " + strconv.Quote(model) + " are busy; try again"}
}

func slotContext(r registry.ReplicaRecord) int {
	if r.MaxConcurrent <= 0 || r.ContextWindow <= 0 {
		return 0
	}
	return r.ContextWindow / r.MaxConcurrent
}

// forward sends the request to rep and relays the reply. It returns true once
// the reply has started reaching the client. It returns false only if nothing
// was written, so the caller may retry.
func (g *gateway) forward(w http.ResponseWriter, r *http.Request, rep registry.ReplicaRecord, raw []byte, policy, failover string) bool {
	target := strings.TrimSuffix(rep.Endpoint, "/") + "/v1/chat/completions"
	req, err := postJSON(r.Context(), target, raw)
	if err != nil {
		log.Printf("route %s: %v", rep.ReplicaID, err)
		return false
	}
	if rep.NodeID != g.leaderID {
		u, _ := url.Parse(target)
		ts, sig := control.SignForward(g.secret, http.MethodPost, u.Path, raw, g.now())
		req.Header.Set(control.HeaderTimestamp, ts)
		req.Header.Set(control.HeaderSignature, sig)
	}

	resp, err := upstreamClient.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("route %s: %v", rep.ReplicaID, err)
		}
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		log.Printf("route %s: upstream returned %d; trying another replica", rep.ReplicaID, resp.StatusCode)
		return false
	}

	w.Header().Set("X-Meridian-Worker", rep.ReplicaID)
	w.Header().Set("X-Meridian-Model", rep.Model)
	w.Header().Set("X-Meridian-Routing-Policy", policy)
	if failover != "" {
		w.Header().Set("X-Meridian-Failover", failover)
	}
	relay(w, resp)
	return true
}

func (g *gateway) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	out := api.StatusResponse{Workers: []api.WorkerStatus{}}
	for _, rep := range g.reg.Replicas() {
		port := 0
		if u, err := url.Parse(rep.Endpoint); err == nil {
			port, _ = strconv.Atoi(u.Port())
		}
		out.Workers = append(out.Workers, api.WorkerStatus{
			ID:                rep.ReplicaID,
			Model:             rep.Model,
			Port:              port,
			State:             string(rep.State),
			Healthy:           rep.Healthy,
			InFlight:          rep.InFlight,
			Served:            rep.Served,
			EstTokensInFlight: rep.EstTokensInFlight,
			TokPerSec:         rep.TokPerSec,
			LastCheck:         formatTime(rep.LastCheck),
			LastError:         rep.LastError,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

func (g *gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	list := api.ModelList{Object: "list", Data: []api.ModelInfo{}}
	for _, m := range g.models() {
		if g.reg.CountHealthy(m) > 0 {
			list.Data = append(list.Data, api.ModelInfo{ID: m, Object: "model"})
		}
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func (g *gateway) handleCatalog(w http.ResponseWriter, r *http.Request) {
	out := api.CatalogResponse{
		LLM:                  []api.CatalogModel{},
		ML:                   []api.CatalogModel{},
		RoutingPolicies:      g.router.Policies(),
		DefaultRoutingPolicy: g.router.Default(),
	}
	for _, m := range g.models() {
		reps := g.reg.ReplicasForModel(m)
		healthy, loading, ctx := 0, 0, 0
		nodes := map[string]bool{}
		for _, r := range reps {
			nodes[r.NodeID] = true
			if r.Healthy && r.State == registry.ReplicaReady {
				healthy++
			}
			if r.State == registry.ReplicaLoading {
				loading++
			}
			if r.ContextWindow > ctx {
				ctx = r.ContextWindow
			}
		}
		state := "unhealthy"
		switch {
		case healthy > 0:
			state = "healthy"
		case loading > 0:
			state = "starting"
		}
		out.LLM = append(out.LLM, api.CatalogModel{
			ID:             m,
			Object:         "model",
			Kind:           "llm",
			State:          state,
			Healthy:        healthy > 0,
			Workers:        len(reps),
			HealthyWorkers: healthy,
			ContextWindow:  ctx,
			Description:    fmt.Sprintf("%d replicas on %d nodes", len(reps), len(nodes)),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusOK, out)
}

func (g *gateway) handlePolicies(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, api.RoutingPolicyList{Default: g.router.Default(), Policies: g.router.Policies()})
}

func (g *gateway) handleSetDefault(w http.ResponseWriter, r *http.Request) {
	var req api.PolicyUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "body must be {\"name\":\"<policy>\"}")
		return
	}
	if err := g.router.SetDefault(strings.TrimSpace(req.Name)); err != nil {
		api.WriteError(w, http.StatusBadRequest, "unknown_routing_policy", err.Error())
		return
	}
	g.handlePolicies(w, r)
}

// models returns every model name in the registry, sorted.
func (g *gateway) models() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range g.reg.Replicas() {
		if !seen[r.Model] {
			seen[r.Model] = true
			out = append(out, r.Model)
		}
	}
	sort.Strings(out)
	return out
}
