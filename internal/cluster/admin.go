package cluster

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// admin guards everything that changes or inspects the whole system: the
// routing policy, the control panel and its live event stream. Access is by
// bearer token. With no token configured, admin access is disabled.
type admin struct {
	token []byte
}

func newAdmin(token string) *admin {
	return &admin{token: []byte(token)}
}

func (a *admin) enabled() bool { return len(a.token) > 0 }

func (a *admin) authorized(r *http.Request) bool {
	if !a.enabled() {
		return false
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	got := []byte(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
	return subtle.ConstantTimeCompare(got, a.token) == 1
}

// require wraps an admin-only handler.
func (a *admin) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled() {
			api.WriteError(w, http.StatusForbidden, "admin_disabled",
				"admin access is disabled; start the leader with --admin-token")
			return
		}
		if !a.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="meridian-admin"`)
			api.WriteError(w, http.StatusUnauthorized, "unauthorized", "admin token required")
			return
		}
		next(w, r)
	}
}

// adminCluster is the control-panel view: every node and its replicas,
// including IP addresses and process IDs. It is never sent to regular users.
type adminCluster struct {
	LeaderID string                  `json:"leader_id"`
	Policy   string                  `json:"policy"`
	Nodes    []adminNode             `json:"nodes"`
	Policies []api.RoutingPolicyInfo `json:"policies"`
}

type adminNode struct {
	NodeID     string         `json:"node_id"`
	Address    string         `json:"address"`
	HTTPPort   int            `json:"http_port"`
	Local      bool           `json:"local"`
	State      string         `json:"state"`
	CPUCores   int            `json:"cpu_cores"`
	CPUPercent float64        `json:"cpu_percent"`
	RAMUsedMB  int64          `json:"ram_used_mb"`
	RAMTotalMB int64          `json:"ram_total_mb"`
	Replicas   []adminReplica `json:"replicas"`
}

type adminReplica struct {
	ID        string  `json:"id"`
	Model     string  `json:"model"`
	Instance  int     `json:"instance"`
	PID       int     `json:"pid"`
	State     string  `json:"state"`
	LastState string  `json:"last_state,omitempty"`
	Healthy   bool    `json:"healthy"`
	InFlight  int64   `json:"in_flight"`
	Served    int64   `json:"served"`
	TokPerSec float64 `json:"tok_per_sec"`
	Endpoint  string  `json:"endpoint"`
	LastError string  `json:"last_error"`
}

func (g *gateway) handleAdminCluster(w http.ResponseWriter, r *http.Request) {
	out := adminCluster{
		LeaderID: g.leaderID,
		Policy:   g.router.Default(),
		Policies: g.router.Policies(),
		Nodes:    []adminNode{},
	}
	for _, n := range g.reg.Nodes() {
		node := adminNode{
			NodeID:     n.NodeID,
			Address:    n.Address,
			HTTPPort:   n.HTTPPort,
			Local:      n.NodeID == g.leaderID,
			State:      string(n.State),
			CPUCores:   n.Hardware.CPUCores,
			CPUPercent: n.CPUPercent,
			RAMUsedMB:  n.RAMUsedMB,
			RAMTotalMB: n.Hardware.RAMTotalMB,
			Replicas:   []adminReplica{},
		}
		for _, rep := range g.reg.ReplicasOnNode(n.NodeID) {
			node.Replicas = append(node.Replicas, adminReplica{
				ID:        rep.ReplicaID,
				Model:     rep.Model,
				Instance:  rep.Instance,
				PID:       rep.PID,
				State:     string(rep.State),
				LastState: string(rep.LastState),
				Healthy:   rep.Healthy,
				InFlight:  rep.InFlight,
				Served:    rep.Served,
				TokPerSec: rep.TokPerSec,
				Endpoint:  rep.Endpoint,
				LastError: rep.LastError,
			})
		}
		out.Nodes = append(out.Nodes, node)
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusOK, out)
}

// handleAdminEvents streams route events as server-sent events. It sends a
// comment every 15 s so proxies keep the connection open.
func (g *gateway) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "streaming is not supported")
		return
	}
	events, unsubscribe := g.hub.subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		case e := <-events:
			b, _ := json.Marshal(e)
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}
	}
}
