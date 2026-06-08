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

	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
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
	// VS, so each one stays active for the whole run. Every entry must
	// resolve to a virtual server defined in the corpus.
	//
	// Each entry is written either as a bare string or as a mapping. The
	// bare-string form pins the VS but lets the fuzzer randomise its
	// allowed sources on every update. The mapping form additionally pins
	// the allowed sources so they stay constant for the whole run:
	//
	//	fixed_virtual_services:
	//	  - "10.0.0.1:80"
	//	  - vs: "[2001:db8::1]:443/tcp"
	//	    allowed_sources:
	//	      - "2001:db8::/48"
	//
	// The VS spec is "addr:port" or "addr:port/proto" (proto defaults to
	// tcp); IPv6 addresses use the bracketed form. Every pinned allowed
	// source is a CIDR whose address family matches the VS address family.
	FixedVirtualServices []FixedVS `yaml:"fixed_virtual_services"`
}

// FixedVS is a single fixed virtual service entry. VS is the textual VS
// spec; AllowedSources optionally pins the allowed-source CIDRs so the
// fuzzer keeps them constant instead of regenerating them on every update.
type FixedVS struct {
	VS             string   `yaml:"vs"`
	AllowedSources []string `yaml:"allowed_sources"`
}

// UnmarshalYAML accepts both the bare-string form and the mapping form. A
// scalar node is treated as the VS spec with no pinned allowed sources; a
// mapping node is decoded into the full structure.
func (m *FixedVS) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&m.VS)
	}
	type rawFixedVS FixedVS
	var raw rawFixedVS
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*m = FixedVS(raw)
	return nil
}

// FixedVSEntry is a parsed fixed virtual service: its canonical key plus
// the pinned allowed-source CIDRs, if any.
type FixedVSEntry struct {
	Key            VsKey
	AllowedSources []CIDR
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
	if _, err := m.FixedVSEntries(); err != nil {
		return err
	}
	return nil
}

// FixedVSEntries parses the configured fixed virtual services into keys and
// pinned allowed-source CIDRs. It returns an error when any VS spec or
// pinned CIDR is malformed, including when a CIDR family does not match the
// VS address family. The keys are not checked against the corpus here; the
// runner performs that cross-check once the corpus has been parsed.
func (m *RuntimeConfig) FixedVSEntries() ([]FixedVSEntry, error) {
	if len(m.FixedVirtualServices) == 0 {
		return nil, nil
	}
	out := make([]FixedVSEntry, 0, len(m.FixedVirtualServices))
	for _, entry := range m.FixedVirtualServices {
		key, err := parseFixedVS(entry.VS)
		if err != nil {
			return nil, fmt.Errorf("fixed_virtual_services: %w", err)
		}
		vsIsIPv4 := net.IP(key.IP[:]).To4() != nil
		sources := make([]CIDR, 0, len(entry.AllowedSources))
		for _, spec := range entry.AllowedSources {
			cidr, err := parseAllowedSourceCIDR(spec, vsIsIPv4)
			if err != nil {
				return nil, fmt.Errorf("fixed_virtual_services: %w", err)
			}
			sources = append(sources, cidr)
		}
		out = append(out, FixedVSEntry{Key: key, AllowedSources: sources})
	}
	return out, nil
}

// FixedVSKeys parses the configured fixed virtual services into VS keys,
// discarding any pinned allowed sources. It is a convenience wrapper around
// FixedVSEntries for callers that only need the identities.
func (m *RuntimeConfig) FixedVSKeys() ([]VsKey, error) {
	entries, err := m.FixedVSEntries()
	if err != nil {
		return nil, err
	}
	out := make([]VsKey, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Key)
	}
	return out, nil
}

// parseAllowedSourceCIDR parses a pinned allowed-source CIDR. The address
// family must match the VS VIP family, since the dataplane requires
// allowed sources to share the VS family. Addresses are encoded in the
// same byte widths the operation generator uses: four bytes for IPv4,
// sixteen for IPv6.
func parseAllowedSourceCIDR(spec string, vsIsIPv4 bool) (CIDR, error) {
	s := strings.TrimSpace(spec)
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		return CIDR{}, fmt.Errorf("invalid allowed source %q: %w", spec, err)
	}
	cidrIsIPv4 := network.IP.To4() != nil
	if cidrIsIPv4 != vsIsIPv4 {
		return CIDR{}, fmt.Errorf(
			"allowed source %q family does not match the virtual service family",
			spec,
		)
	}
	if cidrIsIPv4 {
		return CIDR{
			Addr: append([]byte(nil), network.IP.To4()...),
			Mask: append([]byte(nil), network.Mask...),
		}, nil
	}
	return CIDR{
		Addr: append([]byte(nil), network.IP.To16()...),
		Mask: append([]byte(nil), network.Mask...),
	}, nil
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
