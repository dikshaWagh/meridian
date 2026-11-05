package ml

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// DefaultListen is the ML subsystem's own listener. The ML surface is served on
// a separate port so the LLM balancer's server, mux and routes stay untouched.
const DefaultListen = "0.0.0.0:8090"

// Options configures the ML subsystem. It is independent of balancer.Options.
type Options struct {
	Listen            string
	ConfigPath        string
	Workers           []WorkerSpec
	Weights           Weights
	HealthInterval    time.Duration
	TelemetryInterval time.Duration
	RequestTimeout    time.Duration
	StartupGrace      time.Duration
	FailureThreshold  int
	Logger            *log.Logger
}

// Subsystem is a wired ML balancer: pool, router, health checks and telemetry.
type Subsystem struct {
	Pool      *Pool
	Router    *Router
	Health    *HealthChecker
	Telemetry *Telemetry
	Handler   http.Handler
	Listen    string
	logger    *log.Logger
}

func (o Options) withDefaults() Options {
	if o.Listen == "" {
		o.Listen = DefaultListen
	}
	if o.Weights.Validate() != nil {
		o.Weights = DefaultWeights
	}
	if o.HealthInterval <= 0 {
		o.HealthInterval = DefaultHealthInterval
	}
	if o.TelemetryInterval <= 0 {
		o.TelemetryInterval = DefaultTelemetryInterval
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = DefaultRequestTimeout
	}
	if o.StartupGrace <= 0 {
		o.StartupGrace = DefaultStartupGrace
	}
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = DefaultFailureThreshold
	}
	if o.Logger == nil {
		o.Logger = log.New(os.Stderr, "[ml] ", log.LstdFlags)
	}
	return o
}

// New wires the ML subsystem. Workers come from Options.Workers, or from the
// YAML config file when Workers is empty.
func New(o Options) (*Subsystem, error) {
	if o.ConfigPath != "" && len(o.Workers) == 0 {
		cfg, err := LoadConfig(o.ConfigPath)
		if err != nil {
			return nil, err
		}
		o.Workers = cfg.Specs()
		if cfg.Listen != "" && o.Listen == "" {
			o.Listen = cfg.Listen
		}
		if cfg.Weights != nil {
			o.Weights = *cfg.Weights
		}
		o.HealthInterval = cfg.HealthInterval.orDefault(o.HealthInterval)
		o.TelemetryInterval = cfg.TelemetryInterval.orDefault(o.TelemetryInterval)
		o.RequestTimeout = cfg.RequestTimeout.orDefault(o.RequestTimeout)
		o.StartupGrace = cfg.StartupGrace.orDefault(o.StartupGrace)
		if cfg.FailureThreshold > 0 {
			o.FailureThreshold = cfg.FailureThreshold
		}
	}
	o = o.withDefaults()
	if len(o.Workers) == 0 {
		return nil, errors.New("no ml workers configured (use --ml-config or --ml-worker)")
	}
	pool, err := NewPool(o.Workers, o.StartupGrace, o.FailureThreshold)
	if err != nil {
		return nil, err
	}
	router := NewRouter(pool, o.Weights)
	telemetry := NewTelemetry(pool, o.TelemetryInterval)
	return &Subsystem{
		Pool:      pool,
		Router:    router,
		Health:    NewHealthChecker(pool, o.HealthInterval, o.Logger),
		Telemetry: telemetry,
		Handler:   NewHandler(router, telemetry, o.RequestTimeout, o.Logger),
		Listen:    o.Listen,
		logger:    o.Logger,
	}, nil
}

// Start begins health checks and telemetry sampling.
func (s *Subsystem) Start(ctx context.Context) {
	s.Health.Start(ctx)
	s.Telemetry.Start(ctx)
}

// Serve runs the ML subsystem until ctx is cancelled. It owns its own HTTP
// server; the LLM balancer's server is never modified or shared.
func (s *Subsystem) Serve(ctx context.Context) error {
	s.Start(ctx)
	srv := &http.Server{Addr: s.Listen, Handler: s.Handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		s.Pool.StopAll("meridian shutting down")
	}()
	models := s.Pool.Models()
	s.logger.Printf("ml inference balancer listening on %s (%d workers, models: %v)", s.Listen, len(s.Pool.Workers()), models)
	s.logger.Printf("ml routing weights: latency=%.2f queue=%.2f gpu=%.2f cpu=%.2f",
		s.Router.Weights().Latency, s.Router.Weights().Queue, s.Router.Weights().GPU, s.Router.Weights().CPU)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ml listener: %w", err)
	}
	return nil
}

// Run wires and serves the ML subsystem.
func Run(ctx context.Context, o Options) error {
	s, err := New(o)
	if err != nil {
		return err
	}
	return s.Serve(ctx)
}
