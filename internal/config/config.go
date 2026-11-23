// Package config parses and validates the `serve` command line, including the
// cluster roles (--leader, --worker). Standalone flags keep their names and
// defaults from before the cluster work.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/balancer"
)

// Role selects what the process does for the cluster.
type Role string

const (
	RoleStandalone Role = "standalone"
	RoleLeader     Role = "leader"
	RoleWorker     Role = "worker"
)

// SecretEnv is the environment variable used when --cluster-secret is unset.
const SecretEnv = "MERIDIAN_CLUSTER_SECRET"

const (
	defaultDiscoveryPort    = 9999
	defaultControlPort      = 9000
	defaultReplicaPortBase  = 8100
	maxNodeIDLen            = 64
	maxPort                 = 65535
	defaultHeartbeat        = 5 * time.Second
	defaultNodeTimeout      = 15 * time.Second
	defaultHealthInterval   = 2 * time.Second
	defaultDiscoveryWait    = 20 * time.Second
	defaultDiscoveryTimeout = 60 * time.Second
	defaultMaxBackoff       = 30 * time.Second
)

// ClusterConfig holds the settings used only by --leader and --worker.
type ClusterConfig struct {
	NodeID              string
	StateDir            string
	Secret              string
	DiscoveryPort       int
	ControlPort         int
	AdvertiseIP         string
	LeaderAddr          string
	DiscoveryTimeout    time.Duration
	HeartbeatInterval   time.Duration
	NodeTimeout         time.Duration
	HealthInterval      time.Duration
	DiscoveryInterval   time.Duration
	ReconnectMaxBackoff time.Duration
	ReplicaPortBase     int
}

// ServeConfig is the resolved `serve` command.
type ServeConfig struct {
	Role    Role
	Cluster ClusterConfig
	Options balancer.Options
}

// LLMFlags implements flag.Value for the repeatable --llm flag.
// Format: "model.gguf:2" or "model.gguf" (defaults to 1 instance).
type LLMFlags []string

func (f *LLMFlags) String() string {
	return strings.Join(*f, ",")
}

func (f *LLMFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// ToConfigs parses "model.gguf[:instances]" values. The count follows the last
// colon; a missing count means one instance, and a malformed count is an error.
func (f LLMFlags) ToConfigs() ([]balancer.LLMConfig, error) {
	var configs []balancer.LLMConfig
	for _, raw := range f {
		value := strings.TrimSpace(raw)
		model, workers := value, 1
		if i := strings.LastIndex(value, ":"); i >= 0 {
			n, err := strconv.Atoi(strings.TrimSpace(value[i+1:]))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("--llm %q: worker count must be a positive integer", raw)
			}
			model, workers = strings.TrimSpace(value[:i]), n
		}
		if model == "" {
			return nil, fmt.Errorf("--llm %q: missing model file", raw)
		}
		configs = append(configs, balancer.LLMConfig{Model: model, Workers: workers})
	}
	return configs, nil
}

// clusterOnlyFlags maps each cluster flag to the roles it applies to. Setting
// one of these with a role that does not use it is an error, so a typo or a
// misplaced flag is caught at startup instead of being silently ignored.
var clusterOnlyFlags = map[string]Role{
	"node-id":               "",
	"state-dir":             "",
	"cluster-secret":        "",
	"discovery-port":        "",
	"control-port":          "",
	"advertise-ip":          "",
	"replica-port-base":     "",
	"leader-addr":           RoleWorker,
	"discovery-timeout":     RoleWorker,
	"heartbeat-interval":    RoleWorker,
	"reconnect-max-backoff": RoleWorker,
	"node-timeout":          RoleLeader,
	"health-interval":       RoleLeader,
	"discovery-interval":    RoleLeader,
}

// Parse reads the arguments that follow `serve`. lookupEnv supplies the secret
// fallback. It returns flag.ErrHelp when -h is passed, and it does not print
// the secret or any other value.
func Parse(args []string, lookupEnv func(string) (string, bool)) (*ServeConfig, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	o := balancer.Options{}
	c := ClusterConfig{}
	var leader, worker bool

	fs.BoolVar(&leader, "leader", false, "run as cluster leader: discovery, registry, public API, local models")
	fs.BoolVar(&worker, "worker", false, "run as cluster worker: registers with the leader, heartbeats, serves local models")
	fs.StringVar(&c.NodeID, "node-id", "", "stable node ID (default: read or create <state-dir>/node-id)")
	fs.StringVar(&c.StateDir, "state-dir", "~/.meridian", "directory for node-id and other state")
	fs.StringVar(&c.Secret, "cluster-secret", "", "shared secret for signing cluster messages (default: $"+SecretEnv+"); never logged")
	fs.IntVar(&c.DiscoveryPort, "discovery-port", defaultDiscoveryPort, "UDP port for DISCOVER_LEADER / LEADER_FOUND")
	fs.IntVar(&c.ControlPort, "control-port", defaultControlPort, "TCP port for registration, heartbeats and control")
	fs.StringVar(&o.Host, "host", "0.0.0.0", "balancer bind address")
	fs.IntVar(&o.Port, "port", 8080, "balancer port")
	fs.StringVar(&c.AdvertiseIP, "advertise-ip", "", "IP other nodes use to reach this node (default: first non-loopback IPv4)")
	fs.StringVar(&c.LeaderAddr, "leader-addr", "", "host:port of the leader control plane; skips UDP discovery (worker only)")
	fs.DurationVar(&c.DiscoveryTimeout, "discovery-timeout", defaultDiscoveryTimeout, "give up one discovery round after this long (worker only)")
	fs.DurationVar(&c.HeartbeatInterval, "heartbeat-interval", defaultHeartbeat, "how often the worker sends HEARTBEAT (worker only)")
	fs.DurationVar(&c.NodeTimeout, "node-timeout", defaultNodeTimeout, "no heartbeat for this long marks a node offline (leader only)")
	fs.DurationVar(&c.HealthInterval, "health-interval", defaultHealthInterval, "replica /health probe interval (leader only)")
	fs.DurationVar(&c.DiscoveryInterval, "discovery-interval", defaultDiscoveryWait, "full registry sweep interval (leader only)")
	fs.DurationVar(&c.ReconnectMaxBackoff, "reconnect-max-backoff", defaultMaxBackoff, "upper bound for worker reconnect backoff (worker only)")
	fs.IntVar(&c.ReplicaPortBase, "replica-port-base", defaultReplicaPortBase, "first loopback port for local replicas in cluster modes")

	fs.StringVar(&o.ModelsDir, "models-dir", "~/models", "GGUF model directory")
	fs.StringVar(&o.LlamaBin, "llama-bin", "llama-server", "llama-server binary")
	fs.IntVar(&o.Threads, "threads", 4, "threads per worker")
	fs.IntVar(&o.Context, "ctx", 8192, "worker context size")
	fs.IntVar(&o.Parallel, "parallel", 2, "parallel slots per worker")
	fs.IntVar(&o.NGL, "ngl", 0, "GPU layers per worker (0 = CPU only)")
	fs.IntVar(&o.GPUWorkers, "gpu-workers", 1, "number of workers to offload to GPU")
	fs.StringVar(&o.BackendLib, "backend-lib", "", "llama.cpp backend .so (e.g. Vulkan)")
	fs.StringVar(&o.GPUDevice, "gpu-device", "", "device name, e.g. Vulkan1")
	fs.StringVar(&o.RoutingPolicy, "routing-policy", "round-robin", "default routing policy (see /policies)")
	fs.BoolVar(&o.EnableLLM, "enable-llm", true, "enable the LLM serving subsystem")
	fs.BoolVar(&o.EnableML, "enable-ml", false, "enable the OCR serving subsystem")
	fs.StringVar(&o.LLMModel, "llm-model", "", "[DEPRECATED] serve one GGUF model file instead of the built-in worker set (use --llm instead)")
	fs.StringVar(&o.LLMModelName, "llm-model-name", "", "[DEPRECATED] public model id for --llm-model (use --llm instead)")
	fs.IntVar(&o.LLMWorkers, "llm-workers", 1, "[DEPRECATED] number of workers for --llm-model (use --llm instead)")
	fs.StringVar(&o.MLConfig, "ml-config", "configs/ml.yaml", "path to the ML YAML configuration")
	fs.IntVar(&o.RAMHeadroomMB, "ram-headroom-mb", 1024, "RAM kept free after admitting all LLM instances")
	fs.BoolVar(&o.RestartWorkers, "restart-workers", true, "restart a worker process automatically when it exits")
	fs.IntVar(&o.MaxRestarts, "max-restarts", 5, "consecutive failed restarts before a worker is left down")

	var llmFlags LLMFlags
	fs.Var(&llmFlags, "llm", "model file with optional instance count, e.g. qwen.gguf:2 (repeat for each model; count defaults to 1)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if leader && worker {
		return nil, errors.New("--leader and --worker are mutually exclusive")
	}
	if o.LLMModel != "" && len(llmFlags) > 0 {
		return nil, errors.New("use either --llm or the deprecated --llm-model, not both")
	}
	configs, err := llmFlags.ToConfigs()
	if err != nil {
		return nil, err
	}
	if o.LLMModel != "" && len(configs) == 0 {
		// Same conversion balancer.Run applies, done here so cluster modes see it too.
		if o.LLMWorkers < 1 {
			o.LLMWorkers = 1
		}
		configs = []balancer.LLMConfig{{Model: o.LLMModel, Workers: o.LLMWorkers}}
	}
	o.LLMConfigs = configs

	cfg := &ServeConfig{Role: RoleStandalone, Cluster: c, Options: o}
	switch {
	case leader:
		cfg.Role = RoleLeader
	case worker:
		cfg.Role = RoleWorker
	}
	if cfg.Role != RoleStandalone {
		// Cluster replicas start at --replica-port-base. Standalone keeps its own port range.
		cfg.Options.ReplicaPortBase = c.ReplicaPortBase
	}

	var setClusterFlags []string
	fs.Visit(func(f *flag.Flag) {
		if _, ok := clusterOnlyFlags[f.Name]; ok {
			setClusterFlags = append(setClusterFlags, f.Name)
		}
	})
	if cfg.Role == RoleStandalone {
		if len(setClusterFlags) > 0 {
			return nil, fmt.Errorf("--%s requires --leader or --worker", setClusterFlags[0])
		}
	} else {
		for _, name := range setClusterFlags {
			want := clusterOnlyFlags[name]
			if want != "" && want != cfg.Role {
				return nil, fmt.Errorf("--%s applies only to --%s", name, want)
			}
		}
		if cfg.Cluster.Secret == "" {
			if v, ok := lookupEnv(SecretEnv); ok {
				cfg.Cluster.Secret = v
			}
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate checks the resolved configuration. Standalone mode only needs the
// checks that have always applied; the balancer checks the rest on Run.
func (c *ServeConfig) validate() error {
	if c.Role == RoleStandalone {
		return nil
	}
	cl := c.Cluster
	if strings.TrimSpace(cl.Secret) == "" {
		return fmt.Errorf("cluster secret is required in cluster mode: pass --cluster-secret or set %s", SecretEnv)
	}
	if cl.NodeID != "" {
		if err := validateNodeID(cl.NodeID); err != nil {
			return err
		}
	}
	if cl.AdvertiseIP != "" && net.ParseIP(cl.AdvertiseIP) == nil {
		return fmt.Errorf("--advertise-ip %q is not an IP address", cl.AdvertiseIP)
	}
	if cl.LeaderAddr != "" {
		if err := validateHostPort(cl.LeaderAddr); err != nil {
			return fmt.Errorf("--leader-addr: %w", err)
		}
	}

	ports := []struct {
		flag string
		port int
	}{
		{"port", c.Options.Port},
		{"control-port", cl.ControlPort},
		{"discovery-port", cl.DiscoveryPort},
	}
	for _, p := range ports {
		if p.port < 1 || p.port > maxPort {
			return fmt.Errorf("--%s %d is outside 1-%d", p.flag, p.port, maxPort)
		}
	}
	for i := range ports {
		for j := i + 1; j < len(ports); j++ {
			if ports[i].port == ports[j].port {
				return fmt.Errorf("--%s and --%s both use port %d; each must differ", ports[i].flag, ports[j].flag, ports[i].port)
			}
		}
	}

	instances := 0
	for _, l := range c.Options.LLMConfigs {
		instances += max(l.Workers, 1)
	}
	if instances > 0 {
		last := cl.ReplicaPortBase + instances - 1
		if cl.ReplicaPortBase < 1 || last > maxPort {
			return fmt.Errorf("--replica-port-base %d with %d replicas exceeds port range 1-%d", cl.ReplicaPortBase, instances, maxPort)
		}
		for _, p := range ports {
			if p.port >= cl.ReplicaPortBase && p.port <= last {
				return fmt.Errorf("replica ports %d-%d overlap --%s (%d); move --replica-port-base", cl.ReplicaPortBase, last, p.flag, p.port)
			}
		}
	}

	timings := []struct {
		flag string
		d    time.Duration
	}{
		{"heartbeat-interval", cl.HeartbeatInterval},
		{"node-timeout", cl.NodeTimeout},
		{"health-interval", cl.HealthInterval},
		{"discovery-interval", cl.DiscoveryInterval},
		{"discovery-timeout", cl.DiscoveryTimeout},
		{"reconnect-max-backoff", cl.ReconnectMaxBackoff},
	}
	for _, t := range timings {
		if t.d <= 0 {
			return fmt.Errorf("--%s must be greater than zero", t.flag)
		}
	}
	if cl.NodeTimeout <= cl.HeartbeatInterval {
		return fmt.Errorf("--node-timeout (%s) must be longer than --heartbeat-interval (%s)", cl.NodeTimeout, cl.HeartbeatInterval)
	}
	if len(c.Options.LLMConfigs) == 0 {
		return fmt.Errorf("a %s needs at least one local model: pass --llm <file.gguf>[:instances]", c.Role)
	}
	return nil
}

func validateNodeID(id string) error {
	if len(id) > maxNodeIDLen {
		return fmt.Errorf("--node-id is longer than %d characters", maxNodeIDLen)
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("--node-id %q may contain only letters, digits, '.', '-' and '_'", id)
		}
	}
	return nil
}

func validateHostPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q must be host:port", addr)
	}
	if host == "" {
		return fmt.Errorf("%q has an empty host", addr)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > maxPort {
		return fmt.Errorf("%q has an invalid port", addr)
	}
	return nil
}

// Describe returns the resolved cluster settings as lines for the startup log.
// The secret is shown only as set or unset.
func (c *ServeConfig) Describe() []string {
	if c.Role == RoleStandalone {
		return []string{"role: standalone"}
	}
	cl := c.Cluster
	secret := "unset"
	if cl.Secret != "" {
		secret = "set (redacted)"
	}
	lines := []string{
		fmt.Sprintf("role: %s", c.Role),
		fmt.Sprintf("node-id: %s", valueOrAuto(cl.NodeID)),
		fmt.Sprintf("state-dir: %s", cl.StateDir),
		fmt.Sprintf("cluster-secret: %s", secret),
		fmt.Sprintf("advertise-ip: %s", valueOrAuto(cl.AdvertiseIP)),
		fmt.Sprintf("http: %s:%d", c.Options.Host, c.Options.Port),
		fmt.Sprintf("control: tcp :%d", cl.ControlPort),
		fmt.Sprintf("discovery: udp :%d", cl.DiscoveryPort),
		fmt.Sprintf("replica-port-base: %d", cl.ReplicaPortBase),
		fmt.Sprintf("heartbeat-interval: %s, node-timeout: %s, health-interval: %s, discovery-interval: %s",
			cl.HeartbeatInterval, cl.NodeTimeout, cl.HealthInterval, cl.DiscoveryInterval),
	}
	if c.Role == RoleWorker {
		lines = append(lines, fmt.Sprintf("leader-addr: %s", valueOrAuto(cl.LeaderAddr)))
	}
	return lines
}

// Prepare resolves the values that depend on this machine and runs the memory
// admission check. Each node checks its own MemAvailable. Standalone mode
// returns immediately, because balancer.Run already runs its own preflight.
func (c *ServeConfig) Prepare() error {
	if c.Role == RoleStandalone {
		return nil
	}
	if c.Cluster.AdvertiseIP == "" {
		ip, err := DetectAdvertiseIP()
		if err != nil {
			return err
		}
		c.Cluster.AdvertiseIP = ip
	}
	avail, err := ReadMemAvailable()
	if err != nil {
		return fmt.Errorf("RAM admission: %w", err)
	}
	return CheckLocalMemory(c.Options, avail)
}

func valueOrAuto(v string) string {
	if v == "" {
		return "(auto)"
	}
	return v
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
