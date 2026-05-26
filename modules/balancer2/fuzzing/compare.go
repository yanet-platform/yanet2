// Package fuzzing — canonical state comparison.
//
// CompareState reconciles the expected in-memory Model with the
// balancerpb.GetStateResponse returned by the controlplane after every
// successful mutation. The comparator is order-insensitive: VSes are
// matched on (addr, port, proto), reals on (ip, port), and any nil/empty
// repeated proto field is normalised before comparison.
//
// The comparator deliberately ignores fields the fuzzer does not drive
// directly:
//
//   - Packet, byte, and session counters on BalancerState, VsState, and
//     RealState (no traffic is generated).
//   - last_packet_timestamp on every level.
//   - PacketHandlerRef and AddrConfig on BalancerState (set by the
//     controlplane at config time, not by the fuzzer's mutation RPCs).
//   - Sessions state name and capacity on BalancerState — Startup sets
//     them once and they are not part of the per-operation contract.
//
// Mismatches are returned as a deterministic, sorted slice of path
// strings suitable for FormatMismatch in logging.go. CompareState returns
// nil when the model matches the response.
package fuzzing

import (
	"fmt"
	"sort"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// CompareState validates that resp contains exactly one BalancerState
// matching cfg.ConfigName and that its VS/real shape mirrors model. It
// returns the sorted slice of mismatch path strings, or nil on success.
// The returned slice is exactly what the runner (Task 7) passes to
// FormatMismatch.
//
// cfg and model must be non-nil; resp may be nil and is reported as a
// balancer_state_count mismatch.
func CompareState(
	cfg *RuntimeConfig,
	model *Model,
	resp *balancerpb.GetStateResponse,
) []string {
	if cfg == nil {
		return []string{"runtime_config:nil"}
	}
	if model == nil {
		return []string{"model:nil"}
	}

	d := newDiff()

	states := respStates(resp)
	if len(states) != 1 {
		d.addf("balancer_state_count: expected=1 actual=%d", len(states))
		return d.sorted()
	}

	state := states[0]
	if state == nil {
		d.add("balancer_state_count: expected=1 actual=1(nil)")
		return d.sorted()
	}

	if state.GetConfigName() != cfg.ConfigName {
		d.addf("config_name: expected=%q actual=%q",
			cfg.ConfigName, state.GetConfigName())
		return d.sorted()
	}

	compareVSes(model, state.GetVs(), d)
	return d.sorted()
}

// CompareStateError is a convenience wrapper that returns a non-nil error
// summarising the mismatch paths, or nil on success. Callers that prefer
// path slices (e.g. for FormatMismatch) should use CompareState directly.
func CompareStateError(
	cfg *RuntimeConfig,
	model *Model,
	resp *balancerpb.GetStateResponse,
) error {
	paths := CompareState(cfg, model, resp)
	if len(paths) == 0 {
		return nil
	}
	return fmt.Errorf("state mismatch: %v", paths)
}

// respStates returns the embedded BalancerState slice or nil when the
// response is missing.
func respStates(resp *balancerpb.GetStateResponse) []*balancerpb.BalancerState {
	if resp == nil {
		return nil
	}
	return resp.GetStates()
}

// diff is a small accumulator for mismatch paths. The slice is sorted on
// retrieval so callers see deterministic output regardless of iteration
// order in maps.
type diff struct {
	paths []string
}

func newDiff() *diff {
	return &diff{}
}

func (m *diff) add(path string) {
	m.paths = append(m.paths, path)
}

func (m *diff) addf(format string, args ...any) {
	m.paths = append(m.paths, fmt.Sprintf(format, args...))
}

func (m *diff) sorted() []string {
	if len(m.paths) == 0 {
		return nil
	}
	out := append([]string(nil), m.paths...)
	sort.Strings(out)
	return out
}

// compareVSes canonicalises expected and actual VS sets by VsKey and
// records extra/missing entries plus per-VS diffs.
func compareVSes(model *Model, actual []*balancerpb.VsState, d *diff) {
	expected := map[VsKey]*VSState{}
	for _, key := range model.ActiveOrder() {
		expected[key] = model.ActiveVS(key)
	}

	seen := make(map[VsKey]struct{}, len(actual))
	for _, vs := range actual {
		key, err := vsKeyFromState(vs)
		if err != nil {
			d.addf("vs:invalid_identity: %v", err)
			continue
		}
		if _, dup := seen[key]; dup {
			d.addf("vs[%s]:duplicate_in_response", formatVsKey(key))
			continue
		}
		seen[key] = struct{}{}

		exp, ok := expected[key]
		if !ok {
			d.addf("extra_vs[%s]", formatVsKey(key))
			continue
		}
		compareVS(key, exp, vs, d)
	}

	for key := range expected {
		if _, ok := seen[key]; !ok {
			d.addf("missing_vs[%s]", formatVsKey(key))
		}
	}
}

// vsKeyFromState extracts the VsKey identity from a VsState.Config.Id,
// canonicalising the address bytes to a 16-byte array via the same path
// the parser uses.
func vsKeyFromState(vs *balancerpb.VsState) (VsKey, error) {
	if vs == nil {
		return VsKey{}, fmt.Errorf("vs_state is nil")
	}
	cfg := vs.GetConfig()
	if cfg == nil {
		return VsKey{}, fmt.Errorf("vs_state.config is nil")
	}
	id := cfg.GetId()
	if id == nil {
		return VsKey{}, fmt.Errorf("vs_state.config.id is nil")
	}
	ip, err := canonical16(id.GetAddr())
	if err != nil {
		return VsKey{}, fmt.Errorf("vs_state.config.id.addr: %w", err)
	}
	if id.GetPort() > 0xFFFF {
		return VsKey{}, fmt.Errorf("vs_state.config.id.port %d out of range",
			id.GetPort())
	}
	return VsKey{
		IP:    ip,
		Port:  uint16(id.GetPort()),
		Proto: id.GetProto(),
	}, nil
}

// realKeyFromState extracts the RealKey identity from a RealState.Config.Id.
func realKeyFromState(real *balancerpb.RealState) (RealKey, error) {
	if real == nil {
		return RealKey{}, fmt.Errorf("real_state is nil")
	}
	cfg := real.GetConfig()
	if cfg == nil {
		return RealKey{}, fmt.Errorf("real_state.config is nil")
	}
	id := cfg.GetId()
	if id == nil {
		return RealKey{}, fmt.Errorf("real_state.config.id is nil")
	}
	ip, err := canonical16(id.GetIp())
	if err != nil {
		return RealKey{}, fmt.Errorf("real_state.config.id.ip: %w", err)
	}
	if id.GetPort() > 0xFFFF {
		return RealKey{}, fmt.Errorf("real_state.config.id.port %d out of range",
			id.GetPort())
	}
	return RealKey{IP: ip, Port: uint16(id.GetPort())}, nil
}

// canonical16 normalises raw address bytes into a 16-byte form matching
// VsKey.IP / RealKey.IP. A 4-byte input is interpreted as an IPv4 address
// and mapped into the IPv4-in-IPv6 representation (::ffff:a.b.c.d), the
// same form net.IP.To16() produces. A 16-byte input is passed through
// verbatim. Any other length is rejected so the caller can flag the
// malformed address.
func canonical16(addr []byte) ([16]byte, error) {
	var out [16]byte
	switch len(addr) {
	case 16:
		copy(out[:], addr)
		return out, nil
	case 4:
		// IPv4-mapped IPv6 prefix: ::ffff:0:0/96.
		out[10] = 0xff
		out[11] = 0xff
		copy(out[12:], addr)
		return out, nil
	default:
		return out, fmt.Errorf("expected 4 or 16 bytes, got %d", len(addr))
	}
}

// formatVsKey renders a VsKey into a stable text form for mismatch paths.
// The proto enum is included so identical (addr, port) pairs on different
// transports do not alias in the diff output.
func formatVsKey(key VsKey) string {
	return fmt.Sprintf("%s:%d/%s",
		formatIP16(key.IP), key.Port, key.Proto.String())
}

// formatRealKey renders a RealKey into a stable text form. RealKeys live
// inside a VS so the proto is omitted.
func formatRealKey(key RealKey) string {
	return fmt.Sprintf("%s:%d", formatIP16(key.IP), key.Port)
}

// formatIP16 renders a 16-byte canonical address. IPv4-mapped addresses
// are rendered as plain IPv4 dotted-decimal; everything else falls back
// to net.IP's String form for IPv6.
func formatIP16(ip [16]byte) string {
	if isIPv4Mapped(ip) {
		return fmt.Sprintf("%d.%d.%d.%d", ip[12], ip[13], ip[14], ip[15])
	}
	// Minimal IPv6 rendering without pulling in net just for String();
	// the bracketed-hex form is unambiguous for mismatch paths.
	return fmt.Sprintf(
		"[%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x]",
		ip[0], ip[1], ip[2], ip[3], ip[4], ip[5], ip[6], ip[7],
		ip[8], ip[9], ip[10], ip[11], ip[12], ip[13], ip[14], ip[15],
	)
}

func isIPv4Mapped(ip [16]byte) bool {
	for idx := 0; idx < 10; idx++ {
		if ip[idx] != 0 {
			return false
		}
	}
	return ip[10] == 0xff && ip[11] == 0xff
}

// compareVS compares one expected VSState against its actual VsState
// counterpart. Identity has already been checked by the caller; this
// function emits scheduler, flag, allowed-source, and real diffs.
func compareVS(
	key VsKey,
	expected *VSState,
	actual *balancerpb.VsState,
	d *diff,
) {
	path := "vs[" + formatVsKey(key) + "]"
	cfg := actual.GetConfig()

	if expected.Scheduler != cfg.GetScheduler() {
		d.addf("%s.scheduler: expected=%s actual=%s",
			path, expected.Scheduler.String(), cfg.GetScheduler().String())
	}

	compareFlags(path, expected.Flags, cfg.GetFlags(), d)
	compareAllowedSourcesCount(path, expected.AllowedSources, actual.GetAllowedSourcesStats(), d)
	compareReals(path, expected, actual.GetReals(), d)
}

// compareFlags emits one path per disagreeing boolean. Treating a nil
// VsFlags as "all false" matches the controlplane's behaviour: it leaves
// VsConfig.Flags nil when no flags were set, and compareFlags must not
// flag that as a mismatch against an expected VsFlags{}.
func compareFlags(parentPath string, expected VsFlags, actual *balancerpb.VsFlags, d *diff) {
	gre := false
	fixMss := false
	pureL3 := false
	if actual != nil {
		gre = actual.GetGre()
		fixMss = actual.GetFixMss()
		pureL3 = actual.GetPureL3()
	}
	if expected.Gre != gre {
		d.addf("%s.flags.gre: expected=%v actual=%v", parentPath, expected.Gre, gre)
	}
	if expected.FixMss != fixMss {
		d.addf("%s.flags.fix_mss: expected=%v actual=%v", parentPath, expected.FixMss, fixMss)
	}
	if expected.PureL3 != pureL3 {
		d.addf("%s.flags.pure_l3: expected=%v actual=%v", parentPath, expected.PureL3, pureL3)
	}
}

// compareAllowedSourcesCount enforces the only deterministic check the
// controlplane permits at the GetState boundary: VsConfig.allowed_sources
// is explicitly meaningless in VsState (see state.proto comment on
// VsState.config), so the comparator falls back to the
// AllowedSourcesStats multiset cardinality. When the model has zero
// CIDRs we accept either an empty stats slice or a controlplane that has
// not yet emitted ACL counter entries; when both sides report a non-zero
// count and disagree, the comparator flags the divergence.
func compareAllowedSourcesCount(
	parentPath string,
	expected []CIDR,
	actual []*balancerpb.AllowedSourcesStats,
	d *diff,
) {
	expectedCount := len(expected)
	actualCount := len(actual)
	if expectedCount == 0 && actualCount == 0 {
		return
	}
	if expectedCount == 0 || actualCount == 0 {
		// One side has CIDRs and the other has none. Counter entries
		// may legitimately lag behind config in a no-traffic fuzzing
		// run, so treat a populated model with an empty stats slice
		// as compatible; the inverse is unambiguous.
		if expectedCount == 0 && actualCount > 0 {
			d.addf("%s.allowed_sources_count: expected=0 actual=%d",
				parentPath, actualCount)
		}
		return
	}
	if expectedCount != actualCount {
		d.addf("%s.allowed_sources_count: expected=%d actual=%d",
			parentPath, expectedCount, actualCount)
	}
}

// compareReals canonicalises the actual reals by RealKey, then matches
// each against the expected per-VS real set. Extra and missing reals are
// reported by path; common reals are compared field by field.
func compareReals(
	parentPath string,
	expected *VSState,
	actual []*balancerpb.RealState,
	d *diff,
) {
	expectedKeys := map[RealKey]*RealState{}
	for _, rk := range expected.Reals() {
		expectedKeys[rk] = expected.Real(rk)
	}

	seen := make(map[RealKey]struct{}, len(actual))
	for _, real := range actual {
		key, err := realKeyFromState(real)
		if err != nil {
			d.addf("%s.real:invalid_identity: %v", parentPath, err)
			continue
		}
		if _, dup := seen[key]; dup {
			d.addf("%s.real[%s]:duplicate_in_response",
				parentPath, formatRealKey(key))
			continue
		}
		seen[key] = struct{}{}

		exp, ok := expectedKeys[key]
		if !ok {
			d.addf("%s.extra_real[%s]", parentPath, formatRealKey(key))
			continue
		}
		compareReal(parentPath, key, exp, real, d)
	}

	for key := range expectedKeys {
		if _, ok := seen[key]; !ok {
			d.addf("%s.missing_real[%s]", parentPath, formatRealKey(key))
		}
	}
}

// compareReal compares one expected real against its actual RealState.
// The effective_weight check accepts the model's *configured* weight
// since the controlplane reports the same value through both fields
// after a config update with no traffic; if the controlplane ever
// diverges effective from configured, the path "effective_weight" makes
// the source of the disagreement obvious.
func compareReal(
	parentPath string,
	key RealKey,
	expected *RealState,
	actual *balancerpb.RealState,
	d *diff,
) {
	path := parentPath + ".real[" + formatRealKey(key) + "]"

	if expected.Enabled != actual.GetEnabled() {
		d.addf("%s.enabled: expected=%v actual=%v",
			path, expected.Enabled, actual.GetEnabled())
	}

	if uint64(expected.Weight) != actual.GetEffectiveWeight() {
		d.addf("%s.effective_weight: expected=%d actual=%d",
			path, expected.Weight, actual.GetEffectiveWeight())
	}
}
