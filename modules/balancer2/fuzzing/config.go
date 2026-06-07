// Package fuzzing implements a standalone load/fuzzing application for the
// balancer2 controlplane. This file defines the sidecar YAML runtime
// configuration and its validation.
package fuzzing

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// RuntimeConfig is the sidecar YAML runtime configuration loaded by the
// fuzzing CLI. Field names and YAML tags match the schema documented in the
// plan; any change here is a breaking change for example configs.
type RuntimeConfig struct {
	// Endpoint is the balancer2 controlplane gRPC address, e.g.
	// "127.0.0.1:8080".
	Endpoint string `yaml:"endpoint"`
	// CorpusPath lists keepalived-style services configuration files used
	// to seed the initial expected model.
	CorpusPath string `yaml:"corpus_path"`
	// ConfigName is the balancer2 named config the fuzzer creates and
	// mutates.
	ConfigName string `yaml:"config_name"`
	// OperationInterval is the wall-clock delay between consecutive
	// operations.
	OperationInterval time.Duration `yaml:"operation_interval"`
	// UpdateVsEvery is cadence N: UpdateVS fires every N operations and
	// DeleteVS fires every 2*N operations.
	UpdateVsEvery uint64 `yaml:"update_vs_every"`
	// StatsInterval controls how often the runner emits periodic latency
	// reports.
	StatsInterval time.Duration `yaml:"stats_interval"`
	// RequestTimeout is the per-RPC context timeout applied to every
	// controlplane call.
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// Seed seeds the deterministic RNG used by operation generation. A
	// zero or missing value causes a non-zero seed to be generated on
	// load and surfaced through EffectiveSeed for replay logging.
	Seed int64 `yaml:"seed"`
	// FixedVirtualServices lists virtual servers that must remain present
	// in every config the fuzzer produces. DeleteVS never removes a fixed
	// VS, so each one stays active for the whole run. Each entry is written
	// as "addr:port" or "addr:port/proto" (proto defaults to tcp); IPv6
	// addresses use the bracketed form, e.g. "[2001:db8::1]:443". Every
	// entry must resolve to a virtual server defined in the corpus.
	FixedVirtualServices []string `yaml:"fixed_virtual_services"`
}

// seedSource is the source of non-zero seeds used when the YAML seed is zero
// or missing. It is package-private so tests can replace it with a
// deterministic source.
var seedSource = defaultSeedSource

// defaultSeedSource returns a non-zero seed derived from the current monotonic
// time. The retry loop guarantees a non-zero return even in the unlikely event
// that the clock reading aliases to zero.
func defaultSeedSource() int64 {
	for {
		v := time.Now().UnixNano()
		if v != 0 {
			return v
		}
	}
}

// LoadRuntimeConfig reads a YAML runtime config from path, decodes it,
// validates it, and applies the effective seed.
func LoadRuntimeConfig(path string) (*RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime config %q: %w", path, err)
	}

	cfg, err := DecodeRuntimeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime config %q: %w", path, err)
	}

	return cfg, nil
}

// DecodeRuntimeConfig decodes a YAML runtime config from raw bytes, validates
// it, and applies the effective seed. It exists so tests can exercise the
// loader without touching the filesystem.
func DecodeRuntimeConfig(data []byte) (*RuntimeConfig, error) {
	cfg := &RuntimeConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse runtime config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cfg.applyEffectiveSeed()
	return cfg, nil
}

// Validate checks that all required fields are populated with usable values.
// Error messages always contain the exact YAML field name so operators can
// correlate failures with their config file.
func (m *RuntimeConfig) Validate() error {
	if m.Endpoint == "" {
		return fmt.Errorf("endpoint: must be set to a non-empty controlplane address")
	}
	if m.CorpusPath == "" {
		return fmt.Errorf("corpus_path required")
	}
	if m.ConfigName == "" {
		return fmt.Errorf("config_name: must be set to a non-empty balancer2 config name")
	}
	if m.OperationInterval <= 0 {
		return fmt.Errorf("operation_interval: must be a positive duration")
	}
	if m.UpdateVsEvery == 0 {
		return fmt.Errorf("update_vs_every: must be greater than zero")
	}
	if m.StatsInterval <= 0 {
		return fmt.Errorf("stats_interval: must be a positive duration")
	}
	if m.RequestTimeout <= 0 {
		return fmt.Errorf("request_timeout: must be a positive duration")
	}
	if _, err := m.FixedVSKeys(); err != nil {
		return err
	}
	return nil
}

// FixedVSKeys parses the configured fixed virtual service specs into VS
// keys. It returns an error when any spec is malformed. The keys are not
// checked against the corpus here; the runner performs that cross-check
// once the corpus has been parsed.
func (m *RuntimeConfig) FixedVSKeys() ([]VsKey, error) {
	if len(m.FixedVirtualServices) == 0 {
		return nil, nil
	}
	out := make([]VsKey, 0, len(m.FixedVirtualServices))
	for _, spec := range m.FixedVirtualServices {
		key, err := parseFixedVS(spec)
		if err != nil {
			return nil, fmt.Errorf("fixed_virtual_services: %w", err)
		}
		out = append(out, key)
	}
	return out, nil
}

// parseFixedVS parses a fixed virtual service spec into a VS key. The
// accepted forms are "addr:port" and "addr:port/proto"; the protocol is
// optional and defaults to TCP, with tcp and udp the only valid values.
// IPv6 addresses use the bracketed host:port form, e.g.
// "[2001:db8::1]:443". Addresses are canonicalised to the 16-byte form so
// the resulting key matches the corpus parser's identities.
func parseFixedVS(spec string) (VsKey, error) {
	var key VsKey
	s := strings.TrimSpace(spec)
	if s == "" {
		return key, fmt.Errorf("empty virtual service spec")
	}

	proto := balancerpb.TransportProto_TCP
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		switch strings.ToLower(s[idx+1:]) {
		case "tcp":
			proto = balancerpb.TransportProto_TCP
		case "udp":
			proto = balancerpb.TransportProto_UDP
		default:
			return key, fmt.Errorf("unsupported protocol in %q (expected tcp or udp)", spec)
		}
		s = s[:idx]
	}

	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return key, fmt.Errorf("invalid address %q: %w", spec, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return key, fmt.Errorf("invalid IP literal %q", host)
	}
	v16 := ip.To16()
	if v16 == nil {
		return key, fmt.Errorf("invalid IP literal %q", host)
	}
	port, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		return key, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if port == 0 || port > 65535 {
		return key, fmt.Errorf("port %d out of range (1..65535)", port)
	}

	copy(key.IP[:], v16)
	key.Port = uint16(port)
	key.Proto = proto
	return key, nil
}

// applyEffectiveSeed assigns a non-zero seed when the YAML seed was zero or
// missing. The chosen value is written back to Seed so callers can log it for
// replay.
func (m *RuntimeConfig) applyEffectiveSeed() {
	if m.Seed != 0 {
		return
	}
	m.Seed = seedSource()
}
