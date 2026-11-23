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
	"strconv"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/config"
	"github.com/dikshaWagh/meridian/internal/control"
	"github.com/dikshaWagh/meridian/internal/discovery"
)

// workerReplicaHealthInterval is the /health probe period for a worker's
// replicas. --health-interval is a leader flag, so workers use the default.
const workerReplicaHealthInterval = 2 * time.Second

// RunWorker runs the worker role until SIGINT or SIGTERM. It starts the local
// replicas, then keeps the control session to the leader up. On shutdown it
// sends DEREGISTER before it stops the replicas.
func RunWorker(cfg *config.ServeConfig) error {
	cl := &cfg.Cluster
	nodeID, err := config.LoadNodeID(cl.StateDir, cl.NodeID)
	if err != nil {
		return fmt.Errorf("node id: %w", err)
	}
	cl.NodeID = nodeID
	secret := []byte(cl.Secret)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	replicas, err := balancer.StartReplicas(cfg.Options, workerReplicaHealthInterval)
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := replicas.Stop(sctx); err != nil {
			log.Printf("stopping local replicas: %v", err)
		}
	}()

	httpAddr := net.JoinHostPort(cfg.Options.Host, strconv.Itoa(cfg.Options.Port))
	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("http port: %w", err)
	}
	srv := &http.Server{Handler: workerMux(nodeID, secret, replicas.Pool), ReadHeaderTimeout: 5 * time.Second}
	httpErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()

	cpu := &cpuSampler{}
	worker := control.NewWorker(control.WorkerConfig{
		NodeID:            nodeID,
		Secret:            secret,
		Address:           cl.AdvertiseIP,
		HTTPPort:          cfg.Options.Port,
		HeartbeatInterval: cl.HeartbeatInterval,
		MaxBackoff:        cl.ReconnectMaxBackoff,
		Snapshot: func() control.WorkerSnapshot {
			return workerSnapshot(nodeID, cl.AdvertiseIP, cfg.Options.Port, replicas.Pool, cfg.Options, cpu)
		},
	})
	resolve := func(ctx context.Context) (string, error) {
		if cl.LeaderAddr != "" {
			return cl.LeaderAddr, nil
		}
		return discovery.FindLeader(ctx, secret, nodeID, cl.DiscoveryPort, cl.DiscoveryTimeout, cl.ReconnectMaxBackoff)
	}

	log.Printf("worker %s up: advertise %s, http :%d, discovery :%d (udp), %d local replicas",
		nodeID, cl.AdvertiseIP, cfg.Options.Port, cl.DiscoveryPort, len(replicas.Pool.Workers()))

	go func() {
		select {
		case <-ctx.Done():
		case err := <-httpErr:
			log.Printf("worker http: %v", err)
			stop()
		}
	}()
	worker.Run(ctx, resolve)

	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(sctx)
	return nil
}

// workerMux serves /healthz and the leader's signed /replicas/{id}/... path.
// Workers have no public API.
func workerMux(nodeID string, secret []byte, pool *balancer.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("POST /replicas/{id}/{rest...}", replicaHandler(nodeID, secret, pool))
	return mux
}
