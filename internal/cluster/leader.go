// Package cluster wires the cluster roles together. Protocol and registry
// logic lives in the packages it calls; this package only starts listeners,
// background loops and shutdown.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/config"
	"github.com/dikshaWagh/meridian/internal/control"
	"github.com/dikshaWagh/meridian/internal/discovery"
	"github.com/dikshaWagh/meridian/internal/health"
	"github.com/dikshaWagh/meridian/internal/registry"
	"github.com/dikshaWagh/meridian/internal/webui"
)

const (
	shutdownTimeout  = 10 * time.Second
	singleLeaderWait = 3 * time.Second
)

// RunLeader runs the leader role until SIGINT or SIGTERM. It binds every
// listener before starting any loop, so a port conflict fails at startup.
func RunLeader(cfg *config.ServeConfig) error {
	cl := &cfg.Cluster
	nodeID, err := config.LoadNodeID(cl.StateDir, cl.NodeID)
	if err != nil {
		return fmt.Errorf("node id: %w", err)
	}
	cl.NodeID = nodeID
	secret := []byte(cl.Secret)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	replicas, err := balancer.StartReplicas(cfg.Options, cl.HealthInterval)
	if err != nil {
		return err
	}
	replicasStopped := false
	stopReplicas := func() {
		if replicasStopped {
			return
		}
		replicasStopped = true
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := replicas.Stop(sctx); err != nil {
			log.Printf("stopping local replicas: %v", err)
		}
	}
	defer stopReplicas()

	found, other, err := discovery.CheckSingleLeader(ctx, secret, nodeID, cl.DiscoveryPort, singleLeaderWait)
	if err != nil {
		return fmt.Errorf("single-leader check: %w", err)
	}
	if other {
		return fmt.Errorf("another leader (%s) is already running at %s:%d; only one leader is allowed per LAN",
			found.LeaderID, found.Address, found.ControlPort)
	}

	reg := registry.New()
	now := time.Now()
	if err := reg.ReplaceNode(localNode(nodeID, cl.AdvertiseIP, cfg.Options.Port, now), localReplicas(nodeID, replicas.Pool, cfg.Options, now)); err != nil {
		return err
	}

	udp, err := discovery.ListenResponder(cl.DiscoveryPort)
	if err != nil {
		return fmt.Errorf("discovery port %d: %w", cl.DiscoveryPort, err)
	}
	defer udp.Close()
	tcp, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(cl.ControlPort)))
	if err != nil {
		return fmt.Errorf("control port %d: %w", cl.ControlPort, err)
	}
	httpAddr := net.JoinHostPort(cfg.Options.Host, strconv.Itoa(cfg.Options.Port))
	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("http port: %w", err)
	}

	router, err := balancer.NewClusterRouter(cfg.Options.RoutingPolicy)
	if err != nil {
		return err
	}
	ctrl := control.NewLeader(control.LeaderConfig{
		NodeID:       nodeID,
		Secret:       secret,
		LeaseSeconds: int(cl.NodeTimeout / time.Second),
	}, reg)
	responder := &discovery.Responder{
		Secret:      secret,
		LeaderID:    nodeID,
		Address:     cl.AdvertiseIP,
		ControlPort: cl.ControlPort,
	}
	cpu := &cpuSampler{}
	monitor := health.New(reg, health.Config{
		LocalNodeID:       nodeID,
		NodeTimeout:       cl.NodeTimeout,
		HealthInterval:    cl.HealthInterval,
		DiscoveryInterval: cl.DiscoveryInterval,
		LocalStats: func() registry.NodeStats {
			return registry.NodeStats{At: time.Now(), CPUPercent: cpu.sample(), RAMUsedMB: ramUsedMB()}
		},
		SyncLocal: func(t time.Time) {
			for _, rec := range localReplicas(nodeID, replicas.Pool, cfg.Options, t) {
				// The registry owns the leader's in-flight and served counts, so
				// keep them. The pool does not see requests routed by the leader.
				if cur, ok := reg.Replica(rec.ReplicaID); ok {
					rec.InFlight, rec.Served = cur.InFlight, cur.Served
				}
				if err := reg.UpsertReplica(rec); err != nil {
					log.Printf("sync local replica %s: %v", rec.ReplicaID, err)
				}
			}
		},
	})

	srv := &http.Server{Handler: leaderHandler(reg, router, secret, nodeID, cl.AdminToken), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	var wg sync.WaitGroup
	run := func(f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				select {
				case errc <- err:
				default:
				}
			}
		}()
	}
	run(func() error { return responder.Serve(udp) })
	run(func() error { return ctrl.Serve(ctx, tcp) })
	run(func() error { monitor.Run(ctx); return nil })
	run(func() error {
		if err := srv.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	log.Printf("leader %s up: advertise %s, http :%d, control :%d (tcp), discovery :%d (udp), %d local replicas",
		nodeID, cl.AdvertiseIP, cfg.Options.Port, cl.ControlPort, cl.DiscoveryPort, len(replicas.Pool.Workers()))

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
	}
	stop()

	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(sctx)
	_ = udp.Close()
	_ = tcp.Close()
	stopReplicas()
	wg.Wait()
	return runErr
}

func ramTotalMB() int64 {
	total, err := config.ReadMemTotal()
	if err != nil {
		return 0
	}
	return total >> 20
}

// ramUsedMB is total minus available, in MiB, for the local-node status row.
func ramUsedMB() int64 {
	total, err := config.ReadMemTotal()
	if err != nil {
		return 0
	}
	avail, err := config.ReadMemAvailable()
	if err != nil || total <= avail {
		return 0
	}
	return (total - avail) >> 20
}

func localNode(nodeID, advertise string, httpPort int, now time.Time) registry.NodeRecord {
	return registry.NodeRecord{
		NodeID:        nodeID,
		Address:       advertise,
		HTTPPort:      httpPort,
		State:         registry.NodeOnline,
		LastHeartbeat: now,
		RegisteredAt:  now,
		Hardware:      registry.Hardware{CPUCores: runtime.NumCPU(), RAMTotalMB: ramTotalMB()},
	}
}

// leaderHandler is the public API: the cluster gateway behind CORS, with the
// embedded web UI served for every other path.
func leaderHandler(reg registry.Registry, router *balancer.ClusterRouter, secret []byte, leaderID, adminToken string) http.Handler {
	gw := newGateway(reg, router, secret, leaderID, adminToken)
	return webui.Handler(balancer.WithCORS(gw.handler()))
}

func statusOf(reg registry.Registry, leaderID string) api.ClusterStatusResponse {
	out := api.ClusterStatusResponse{LeaderID: leaderID, Nodes: []api.NodeStatus{}, Replicas: []api.ReplicaStatus{}}
	for _, n := range reg.Nodes() {
		out.Nodes = append(out.Nodes, api.NodeStatus{
			NodeID:        n.NodeID,
			Address:       n.Address,
			HTTPPort:      n.HTTPPort,
			Local:         n.NodeID == leaderID,
			State:         string(n.State),
			CPUCores:      n.Hardware.CPUCores,
			RAMTotalMB:    n.Hardware.RAMTotalMB,
			CPUPercent:    n.CPUPercent,
			RAMUsedMB:     n.RAMUsedMB,
			LastHeartbeat: formatTime(n.LastHeartbeat),
			RegisteredAt:  formatTime(n.RegisteredAt),
			ReplicaIDs:    append([]string{}, n.ReplicaIDs...),
		})
	}
	for _, r := range reg.Replicas() {
		out.Replicas = append(out.Replicas, api.ReplicaStatus{
			ID:                r.ReplicaID,
			NodeID:            r.NodeID,
			Model:             r.Model,
			Instance:          r.Instance,
			ModelVersion:      r.ModelVersion,
			Kind:              r.Kind,
			Runtime:           r.Runtime,
			Endpoint:          r.Endpoint,
			ContextWindow:     r.ContextWindow,
			MaxConcurrent:     r.MaxConcurrent,
			State:             string(r.State),
			LastState:         string(r.LastState),
			Healthy:           r.Healthy,
			InFlight:          r.InFlight,
			QueueDepth:        r.QueueDepth,
			Served:            r.Served,
			EstTokensInFlight: r.EstTokensInFlight,
			TokPerSec:         r.TokPerSec,
			LastCheck:         formatTime(r.LastCheck),
			LastError:         r.LastError,
		})
	}
	return out
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
