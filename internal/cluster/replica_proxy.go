package cluster

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/control"
)

// replicaHandler serves POST /replicas/{id}/... on a worker. It accepts only
// requests signed by the leader, and it maps the replica ID to the local
// loopback port. It does no selection: the leader already chose this replica.
func replicaHandler(nodeID string, secret []byte, pool *balancer.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatBody))
		if err != nil {
			api.WriteError(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body is too large")
			return
		}
		if err := control.VerifyForward(secret, r.Method, r.URL.Path, body,
			r.Header.Get(control.HeaderTimestamp), r.Header.Get(control.HeaderSignature), time.Now()); err != nil {
			api.WriteError(w, http.StatusForbidden, "forbidden", "leader signature rejected: "+err.Error())
			return
		}

		id := r.PathValue("id")
		var view *replicaView
		for _, v := range replicaViews(nodeID, pool) {
			if v.ID == id {
				v := v
				view = &v
				break
			}
		}
		if view == nil {
			api.WriteError(w, http.StatusNotFound, "replica_not_found", "no replica "+id+" on this node")
			return
		}

		url := fmt.Sprintf("http://127.0.0.1:%d/%s", view.Port, r.PathValue("rest"))
		req, err := postJSON(r.Context(), url, body)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		view.Worker.InFlight.Add(1)
		defer view.Worker.InFlight.Add(-1)

		resp, err := upstreamClient.Do(req)
		if err != nil {
			if !errors.Is(err, r.Context().Err()) {
				log.Printf("replica %s: upstream error: %v", id, err)
			}
			api.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 500 {
			view.Worker.Served.Add(1)
		}
		relay(w, resp)
	})
}
