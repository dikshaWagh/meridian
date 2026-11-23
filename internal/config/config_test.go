package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/balancer"
)

func noEnv(string) (string, bool) { return "", false }

func envWith(secret string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == SecretEnv && secret != "" {
			return secret, true
		}
		return "", false
	}
}

func mustParse(t *testing.T, args []string, env func(string) (string, bool)) *ServeConfig {
	t.Helper()
	cfg, err := Parse(args, env)
	if err != nil {
		t.Fatalf("Parse(%v): unexpected error: %v", args, err)
	}
	return cfg
}

func mustFail(t *testing.T, args []string, env func(string) (string, bool), wantSubstr string) {
	t.Helper()
	_, err := Parse(args, env)
	if err == nil {
		t.Fatalf("Parse(%v): expected error containing %q", args, wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("Parse(%v): error %q does not contain %q", args, err, wantSubstr)
	}
}

// clusterArgs is a valid leader command line; tests override one thing at a time.
func clusterArgs(extra ...string) []string {
	base := []string{
		"--leader", "--cluster-secret", "s3cret",
		"--llm", "qwen.gguf:2",
		"--control-port", "9000", "--discovery-port", "9999", "--port", "8080",
	}
	return append(base, extra...)
}

func TestLLMFlagParsing(t *testing.T) {
	f := LLMFlags{"qwen.gguf:2", "/m/llama-3b.gguf", "mistral.gguf:1"}
	cfgs, err := f.ToConfigs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []struct {
		model   string
		workers int
	}{{"qwen.gguf", 2}, {"/m/llama-3b.gguf", 1}, {"mistral.gguf", 1}}
	if len(cfgs) != len(want) {
		t.Fatalf("expected %d configs, got %d", len(want), len(cfgs))
	}
	for i, w := range want {
		if cfgs[i].Model != w.model || cfgs[i].Workers != w.workers {
			t.Fatalf("config %d: got %+v, want %+v", i, cfgs[i], w)
		}
	}
}

func TestLLMFlagParsingRejectsBadCounts(t *testing.T) {
	for _, v := range []string{"qwen.gguf:0", "qwen.gguf:abc", "qwen.gguf:-1", ":2", "  "} {
		if _, err := (LLMFlags{v}).ToConfigs(); err == nil {
			t.Fatalf("expected error for %q", v)
		}
	}
}

func TestStandaloneDefaultsUnchanged(t *testing.T) {
	cfg := mustParse(t, []string{"--llm", "qwen.gguf:2"}, noEnv)
	if cfg.Role != RoleStandalone {
		t.Fatalf("role = %q, want standalone", cfg.Role)
	}
	o := cfg.Options
	if o.Host != "0.0.0.0" || o.Port != 8080 || o.ModelsDir != "~/models" || o.LlamaBin != "llama-server" {
		t.Fatalf("network/model defaults changed: %+v", o)
	}
	if o.Threads != 4 || o.Context != 8192 || o.Parallel != 2 || o.RAMHeadroomMB != 1024 {
		t.Fatalf("runtime defaults changed: %+v", o)
	}
	if !o.RestartWorkers || o.MaxRestarts != 5 || o.RoutingPolicy != "round-robin" || !o.EnableLLM {
		t.Fatalf("supervision defaults changed: %+v", o)
	}
	if len(o.LLMConfigs) != 1 || o.LLMConfigs[0].Workers != 2 {
		t.Fatalf("LLMConfigs = %+v", o.LLMConfigs)
	}
}

func TestStandaloneConvertsDeprecatedLLMModel(t *testing.T) {
	cfg := mustParse(t, []string{"--llm-model", "a.gguf", "--llm-workers", "3"}, noEnv)
	if len(cfg.Options.LLMConfigs) != 1 || cfg.Options.LLMConfigs[0].Workers != 3 {
		t.Fatalf("LLMConfigs = %+v", cfg.Options.LLMConfigs)
	}
}

func TestLeaderAndWorkerAreMutuallyExclusive(t *testing.T) {
	mustFail(t, []string{"--leader", "--worker", "--cluster-secret", "x", "--llm", "a.gguf"}, noEnv, "mutually exclusive")
}

func TestLLMAndDeprecatedLLMModelConflict(t *testing.T) {
	mustFail(t, []string{"--llm", "a.gguf", "--llm-model", "b.gguf"}, noEnv, "not both")
}

func TestClusterFlagRequiresRole(t *testing.T) {
	mustFail(t, []string{"--control-port", "9100", "--llm", "a.gguf"}, noEnv, "requires --leader or --worker")
}

func TestWorkerOnlyFlagRejectedOnLeader(t *testing.T) {
	mustFail(t, clusterArgs("--leader-addr", "10.0.0.1:9000"), noEnv, "--leader-addr applies only to --worker")
}

func TestLeaderOnlyFlagRejectedOnWorker(t *testing.T) {
	args := []string{"--worker", "--cluster-secret", "x", "--llm", "a.gguf", "--node-timeout", "30s"}
	mustFail(t, args, noEnv, "--node-timeout applies only to --leader")
}

func TestSecretRequiredInClusterMode(t *testing.T) {
	args := []string{"--leader", "--llm", "a.gguf"}
	mustFail(t, args, noEnv, "cluster secret is required")
}

func TestSecretFromEnvironment(t *testing.T) {
	args := []string{"--leader", "--llm", "a.gguf"}
	cfg := mustParse(t, args, envWith("from-env"))
	if cfg.Cluster.Secret != "from-env" {
		t.Fatalf("secret = %q, want value from env", cfg.Cluster.Secret)
	}
}

func TestFlagSecretOverridesEnvironment(t *testing.T) {
	cfg := mustParse(t, clusterArgs(), envWith("from-env"))
	if cfg.Cluster.Secret != "s3cret" {
		t.Fatalf("secret = %q, want flag value", cfg.Cluster.Secret)
	}
}

func TestWhitespaceSecretRejected(t *testing.T) {
	mustFail(t, []string{"--leader", "--cluster-secret", "   ", "--llm", "a.gguf"}, noEnv, "cluster secret is required")
}

func TestPortRangeRejected(t *testing.T) {
	mustFail(t, clusterArgs("--control-port", "70000"), noEnv, "outside 1-65535")
	mustFail(t, clusterArgs("--discovery-port", "0"), noEnv, "outside 1-65535")
}

func TestPortCollisionsRejected(t *testing.T) {
	mustFail(t, clusterArgs("--port", "9000", "--control-port", "9000"), noEnv, "both use port 9000")
	mustFail(t, clusterArgs("--control-port", "9999", "--discovery-port", "9999"), noEnv, "both use port 9999")
	mustFail(t, clusterArgs("--port", "9999"), noEnv, "both use port 9999")
}

func TestReplicaPortRangeMustNotOverlapListeners(t *testing.T) {
	// --llm qwen.gguf:2 uses 8100 and 8101. Moving the base onto --port 8101 must fail.
	mustFail(t, clusterArgs("--replica-port-base", "8100", "--port", "8101"), noEnv, "overlap --port")
}

func TestReplicaPortRangeAboveMaxRejected(t *testing.T) {
	mustFail(t, clusterArgs("--replica-port-base", "65535"), noEnv, "exceeds port range")
}

func TestNodeTimeoutMustExceedHeartbeat(t *testing.T) {
	mustFail(t, clusterArgs("--node-timeout", "5s"), noEnv, "must be longer than --heartbeat-interval")
}

func TestTimingFlagsMustBePositive(t *testing.T) {
	mustFail(t, clusterArgs("--health-interval", "0s"), noEnv, "--health-interval must be greater than zero")
	mustFail(t, clusterArgs("--discovery-interval", "-1s"), noEnv, "--discovery-interval must be greater than zero")
}

func TestLeaderAddrFormat(t *testing.T) {
	args := []string{"--worker", "--cluster-secret", "x", "--llm", "a.gguf"}
	mustFail(t, append(args, "--leader-addr", "10.0.0.1"), noEnv, "must be host:port")
	mustFail(t, append(args, "--leader-addr", ":9000"), noEnv, "empty host")
	mustFail(t, append(args, "--leader-addr", "10.0.0.1:abc"), noEnv, "invalid port")
	cfg := mustParse(t, append(args, "--leader-addr", "10.0.0.1:9000"), noEnv)
	if cfg.Cluster.LeaderAddr != "10.0.0.1:9000" {
		t.Fatalf("LeaderAddr = %q", cfg.Cluster.LeaderAddr)
	}
}

func TestAdvertiseIPMustParse(t *testing.T) {
	mustFail(t, clusterArgs("--advertise-ip", "not-an-ip"), noEnv, "is not an IP address")
	cfg := mustParse(t, clusterArgs("--advertise-ip", "192.168.1.10"), noEnv)
	if cfg.Cluster.AdvertiseIP != "192.168.1.10" {
		t.Fatalf("AdvertiseIP = %q", cfg.Cluster.AdvertiseIP)
	}
}

func TestNodeIDValidation(t *testing.T) {
	mustFail(t, clusterArgs("--node-id", "bad id"), noEnv, "may contain only")
	mustFail(t, clusterArgs("--node-id", strings.Repeat("a", 65)), noEnv, "longer than 64")
	cfg := mustParse(t, clusterArgs("--node-id", "node-01.lab_A"), noEnv)
	if cfg.Cluster.NodeID != "node-01.lab_A" {
		t.Fatalf("NodeID = %q", cfg.Cluster.NodeID)
	}
}

func TestWorkerNeedsLocalModel(t *testing.T) {
	mustFail(t, []string{"--worker", "--cluster-secret", "x"}, noEnv, "at least one local model")
}

func TestLeaderNeedsLocalModel(t *testing.T) {
	mustFail(t, []string{"--leader", "--cluster-secret", "x"}, noEnv, "at least one local model")
}

func TestClusterDefaults(t *testing.T) {
	cfg := mustParse(t, clusterArgs(), noEnv)
	c := cfg.Cluster
	if c.StateDir != "~/.meridian" || c.ReplicaPortBase != 8100 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.HeartbeatInterval != 5*time.Second || c.NodeTimeout != 15*time.Second ||
		c.HealthInterval != 2*time.Second || c.DiscoveryInterval != 20*time.Second ||
		c.DiscoveryTimeout != 60*time.Second || c.ReconnectMaxBackoff != 30*time.Second {
		t.Fatalf("timing defaults: %+v", c)
	}
}

func TestDescribeRedactsSecret(t *testing.T) {
	cfg := mustParse(t, clusterArgs(), noEnv)
	out := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(out, "s3cret") {
		t.Fatalf("Describe leaked the secret:\n%s", out)
	}
	if !strings.Contains(out, "cluster-secret: set (redacted)") {
		t.Fatalf("Describe does not say the secret is set:\n%s", out)
	}
}

func TestHelpReturnsErrHelp(t *testing.T) {
	_, err := Parse([]string{"-h"}, noEnv)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
}

func TestAdmitReplicasListsOverBudget(t *testing.T) {
	const mb = int64(1) << 20
	fps := []Footprint{
		{Name: "small-1", Bytes: 400 * mb},
		{Name: "small-2", Bytes: 400 * mb},
		{Name: "big-1", Bytes: 3000 * mb},
	}
	// Budget is 2000 MB - 256 MB headroom = 1744 MB. The first two fit (800 MB). big-1 does not.
	err := AdmitReplicas(fps, 2000*mb, 256)
	if err == nil {
		t.Fatal("expected admission error")
	}
	if !strings.Contains(err.Error(), "big-1 (3000 MB)") {
		t.Fatalf("error should list big-1: %v", err)
	}
	if strings.Contains(err.Error(), "small-1 (") {
		t.Fatalf("error should not list replicas that fit: %v", err)
	}
}

func TestAdmitReplicasPasses(t *testing.T) {
	const mb = int64(1) << 20
	fps := []Footprint{{Name: "a-1", Bytes: 500 * mb}}
	if err := AdmitReplicas(fps, 1024*mb, 256); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplicaFootprintsCountInstances(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "qwen.gguf"), make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	o := balancer.Options{ModelsDir: dir, LLMConfigs: []balancer.LLMConfig{{Model: "qwen.gguf", Workers: 3}}}
	fps, err := ReplicaFootprints(o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fps) != 3 || fps[0].Name != "qwen-1" || fps[2].Name != "qwen-3" || fps[1].Bytes != 1024 {
		t.Fatalf("footprints = %+v", fps)
	}
}

func TestReplicaFootprintsReportMissingModel(t *testing.T) {
	o := balancer.Options{ModelsDir: t.TempDir(), LLMConfigs: []balancer.LLMConfig{{Model: "nope.gguf", Workers: 1}}}
	if _, err := ReplicaFootprints(o); err == nil || !strings.Contains(err.Error(), "nope.gguf") {
		t.Fatalf("err = %v, want missing model error", err)
	}
}

func TestPrepareStandaloneIsNoop(t *testing.T) {
	cfg := mustParse(t, []string{"--llm", "a.gguf"}, noEnv)
	if err := cfg.Prepare(); err != nil {
		t.Fatalf("standalone Prepare: %v", err)
	}
	if cfg.Cluster.AdvertiseIP != "" {
		t.Fatalf("standalone must not resolve advertise IP, got %q", cfg.Cluster.AdvertiseIP)
	}
}

func TestLoadNodeIDCreatesOnceAndReuses(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadNodeID(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("node id %q is not 128 bits hex", first)
	}
	second, err := LoadNodeID(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("node id changed across loads: %s vs %s", first, second)
	}
	fi, err := os.Stat(filepath.Join(dir, "node-id"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("node-id permissions %v, want 0600", fi.Mode().Perm())
	}
}

func TestLoadNodeIDOverrideWins(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadNodeID(dir, "lab-pc-3")
	if err != nil || id != "lab-pc-3" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node-id")); !os.IsNotExist(err) {
		t.Fatal("override must not create a state file")
	}
}

func TestClusterReplicaPortBaseReachesBalancer(t *testing.T) {
	cfg := mustParse(t, clusterArgs("--replica-port-base", "18100"), noEnv)
	if cfg.Options.ReplicaPortBase != 18100 {
		t.Fatalf("Options.ReplicaPortBase = %d, want 18100", cfg.Options.ReplicaPortBase)
	}
	standalone := mustParse(t, []string{"--llm", "a.gguf"}, noEnv)
	if standalone.Options.ReplicaPortBase != 0 {
		t.Fatalf("standalone ReplicaPortBase = %d, want 0 (legacy 8081 range)", standalone.Options.ReplicaPortBase)
	}
}
