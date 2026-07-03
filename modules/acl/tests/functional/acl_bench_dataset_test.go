package acl_test

// Dataset-driven ACL benchmark: classification throughput on a large,
// production-shaped ruleset under flow-diverse traffic.
//
// Unlike the synthetic benchmarks in acl_bench_test.go, this one exercises tables of
// production scale (hundreds of megabytes, chunked value tables, deep
// tries) and sweeps the concurrent-flow working set via the batch size,
// which is what dominates real firewall performance. By default the
// ruleset and the traffic are synthesized deterministically with a
// production-like shape, so the benchmark runs standalone:
//
//	go test -run '^$' -bench BenchmarkACLDataset -benchtime 1s -count 6 \
//	    ./modules/acl/tests/functional/
//
// Real data can be supplied instead (never committed to the repo):
//
//	ACL_DATASET_RULES=/path/rules.json   ruleset dump {"rules": [...]}
//	ACL_DATASET_PCAP=/path/traffic.pcap  pcap or pcapng traffic sample
//	ACL_DATASET_RULE_COUNT=8192          synthetic ruleset size override
//
// The expensive compile runs once per process and is shared by all
// sub-benchmarks and -count repetitions.

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

type datasetRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

// datasetRule mirrors the JSON schema of ruleset dumps accepted by the
// ACL CLI, so real dumps load without conversion.
type datasetRule struct {
	Actions []struct {
		Kind string `json:"kind"`
	} `json:"actions"`
	Counter string `json:"counter"`
	Devices []struct {
		Name string `json:"name"`
	} `json:"devices"`
	VlanRanges    []datasetRange `json:"vlan_ranges"`
	Srcs          []string       `json:"srcs"`
	Dsts          []string       `json:"dsts"`
	ProtoRanges   []datasetRange `json:"proto_ranges"`
	SrcPortRanges []datasetRange `json:"src_port_ranges"`
	DstPortRanges []datasetRange `json:"dst_port_ranges"`
	Fragment      struct {
		Kind uint32 `json:"kind"`
	} `json:"fragment"`
}

func datasetActionKind(t testing.TB, kind string) uint32 {
	switch kind {
	case "ACTION_KIND_PASS":
		return uint32(cacl.ActionAllow)
	case "ACTION_KIND_DENY":
		return uint32(cacl.ActionDeny)
	case "ACTION_KIND_COUNT":
		return uint32(cacl.ActionCount)
	case "ACTION_KIND_CHECK_STATE", "ACTION_KIND_CREATE_STATE":
		// Stateful actions are neutralized: the benchmark recycles one
		// packet list across rounds and wires no state backend, so
		// per-round state traffic would only distort the measurement.
		return uint32(cacl.ActionCount)
	case "ACTION_KIND_LOG":
		return uint32(cacl.ActionLog)
	default:
		t.Fatalf("unknown action kind %q", kind)
		return 0
	}
}

// parseDatasetNet accepts CIDR ("net/bits"), the addr/mask-address form
// used by non-contiguous production masks, and bare addresses.
func parseDatasetNet(t testing.TB, s string) filter.IPNet {
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		if mask, err := netip.ParseAddr(s[idx+1:]); err == nil {
			addr, err := netip.ParseAddr(s[:idx])
			require.NoError(t, err)
			return filter.IPNet{Addr: addr, Mask: mask}
		}
		return filter.MustParseIPNet(s)
	}
	addr, err := netip.ParseAddr(s)
	require.NoError(t, err)
	if addr.Is4() {
		return filter.IPNet{
			Addr: addr,
			Mask: netip.MustParseAddr("255.255.255.255"),
		}
	}
	return filter.IPNet{
		Addr: addr,
		Mask: netip.MustParseAddr(
			"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		),
	}
}

func convertDatasetRule(t testing.TB, raw datasetRule) cacl.AclRule {
	rule := cacl.AclRule{
		Counter:  raw.Counter,
		Fragment: filter.Fragment(raw.Fragment.Kind),
	}
	for _, action := range raw.Actions {
		rule.Actions = append(
			rule.Actions,
			cacl.AclAction{Kind: datasetActionKind(t, action.Kind)},
		)
	}
	for _, device := range raw.Devices {
		rule.Devices = append(
			rule.Devices, filter.Device{Name: device.Name},
		)
	}
	for _, vlanRange := range raw.VlanRanges {
		rule.VlanRanges = append(
			rule.VlanRanges,
			filter.VlanRange{From: vlanRange.From, To: vlanRange.To},
		)
	}
	for _, address := range raw.Srcs {
		network := parseDatasetNet(t, address)
		if network.Addr.Is4() {
			rule.Src4s = append(rule.Src4s, network)
		} else {
			rule.Src6s = append(rule.Src6s, network)
		}
	}
	for _, address := range raw.Dsts {
		network := parseDatasetNet(t, address)
		if network.Addr.Is4() {
			rule.Dst4s = append(rule.Dst4s, network)
		} else {
			rule.Dst6s = append(rule.Dst6s, network)
		}
	}
	for _, protoRange := range raw.ProtoRanges {
		rule.ProtoRanges = append(
			rule.ProtoRanges,
			filter.ProtoRange{
				From: protoRange.From, To: protoRange.To,
			},
		)
	}
	for _, portRange := range raw.SrcPortRanges {
		rule.SrcPortRanges = append(
			rule.SrcPortRanges,
			filter.PortRange{From: portRange.From, To: portRange.To},
		)
	}
	for _, portRange := range raw.DstPortRanges {
		rule.DstPortRanges = append(
			rule.DstPortRanges,
			filter.PortRange{From: portRange.From, To: portRange.To},
		)
	}
	return rule
}

func loadDatasetRules(t testing.TB, path string) []cacl.AclRule {
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Rules []datasetRule `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	rules := make([]cacl.AclRule, 0, len(doc.Rules))
	for _, raw := range doc.Rules {
		rules = append(rules, convertDatasetRule(t, raw))
	}
	return rules
}

// Proto-range encoding used by rule dumps: one full byte of range per
// protocol number.
var (
	datasetProtoTCP = filter.ProtoRange{From: 6 * 256, To: 6*256 + 255}
	datasetProtoUDP = filter.ProtoRange{From: 17 * 256, To: 17*256 + 255}
)

// randomNet6 derives a nested IPv6 network: a subnet of a random
// supernet with a shape-weighted prefix length.
func randomNet6(r *rand.Rand, supernets []netip.Addr) filter.IPNet {
	base := supernets[r.Intn(len(supernets))].As16()
	bits := 48
	switch pick := r.Intn(100); {
	case pick < 45:
		bits = 48
	case pick < 75:
		bits = 64
	case pick < 90:
		bits = 56
	case pick < 95:
		bits = 40
	default:
		bits = 128
	}
	addr := base
	for idx := 4; idx < (bits+7)/8; idx++ {
		addr[idx] = byte(r.Intn(256))
	}
	mask := [16]byte{}
	for idx := 0; idx < bits/8; idx++ {
		mask[idx] = 0xff
	}
	if bits%8 != 0 {
		mask[bits/8] = byte(0xff << (8 - bits%8))
	}
	for idx := range addr {
		addr[idx] &= mask[idx]
	}
	return filter.IPNet{
		Addr: netip.AddrFrom16(addr),
		Mask: netip.AddrFrom16(mask),
	}
}

func randomNet4(r *rand.Rand) filter.IPNet {
	bits := 16 + r.Intn(17)
	addr := [4]byte{10, byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256))}
	mask := [4]byte{}
	for idx := 0; idx < bits/8; idx++ {
		mask[idx] = 0xff
	}
	if bits%8 != 0 {
		mask[bits/8] = byte(0xff << (8 - bits%8))
	}
	for idx := range addr {
		addr[idx] &= mask[idx]
	}
	return filter.IPNet{
		Addr: netip.AddrFrom4(addr),
		Mask: netip.AddrFrom4(mask),
	}
}

// generateDatasetRules synthesizes a deterministic ruleset whose shape
// follows the production profile this benchmark was calibrated against:
// the vast majority are IPv6 rules with port constraints, sources are
// far more diverse than destinations, prefixes nest inside a small set
// of supernets, and a catch-all deny terminates the set.
func generateDatasetRules(count int) []cacl.AclRule {
	r := rand.New(rand.NewSource(42))

	supernets := make([]netip.Addr, 64)
	for idx := range supernets {
		a := [16]byte{0x2a, 0x02, byte(r.Intn(256)), byte(r.Intn(256))}
		supernets[idx] = netip.AddrFrom16(a)
	}

	srcPool := make([]filter.IPNet, count*35/100+1)
	for idx := range srcPool {
		srcPool[idx] = randomNet6(r, supernets)
	}
	dstPool := make([]filter.IPNet, count*6/100+1)
	for idx := range dstPool {
		dstPool[idx] = randomNet6(r, supernets)
	}

	popularPorts := []uint16{80, 443, 22, 53, 123, 8080, 3128, 25}

	dstPorts := func() []filter.PortRange {
		switch pick := r.Intn(100); {
		case pick < 55:
			p := popularPorts[r.Intn(len(popularPorts))]
			return []filter.PortRange{{From: p, To: p}}
		case pick < 80:
			p := uint16(1 + r.Intn(65000))
			return []filter.PortRange{{From: p, To: p}}
		default:
			from := uint16(1 + r.Intn(60000))
			return []filter.PortRange{
				{From: from, To: from + uint16(r.Intn(1000))},
			}
		}
	}

	proto := func() filter.ProtoRange {
		if r.Intn(100) < 70 {
			return datasetProtoTCP
		}
		return datasetProtoUDP
	}

	action := func() []cacl.AclAction {
		if r.Intn(100) < 70 {
			return []cacl.AclAction{{Kind: uint32(cacl.ActionAllow)}}
		}
		return []cacl.AclAction{
			{Kind: uint32(cacl.ActionCount)},
			{Kind: uint32(cacl.ActionDeny)},
		}
	}

	rules := make([]cacl.AclRule, 0, count+1)
	for idx := range count {
		rule := cacl.AclRule{
			Counter: fmt.Sprintf("dataset_%d", idx),
			Actions: action(),
			VlanRanges: []filter.VlanRange{
				{From: 0, To: 4095},
			},
			ProtoRanges: []filter.ProtoRange{proto()},
		}

		switch pick := r.Intn(1000); {
		case pick < 2:
			rule.Src4s = []filter.IPNet{randomNet4(r)}
			rule.Dst4s = []filter.IPNet{randomNet4(r)}
		case pick < 30:
			rule.Src4s = []filter.IPNet{randomNet4(r)}
			rule.Dst4s = []filter.IPNet{randomNet4(r)}
			rule.DstPortRanges = dstPorts()
		case pick < 130:
			rule.Src6s = []filter.IPNet{
				srcPool[r.Intn(len(srcPool))],
			}
			rule.Dst6s = []filter.IPNet{
				dstPool[r.Intn(len(dstPool))],
			}
		default:
			for idx := 0; idx < 1+r.Intn(3); idx++ {
				rule.Src6s = append(
					rule.Src6s,
					srcPool[r.Intn(len(srcPool))],
				)
			}
			rule.Dst6s = []filter.IPNet{
				dstPool[r.Intn(len(dstPool))],
			}
			rule.DstPortRanges = dstPorts()
		}
		rules = append(rules, rule)
	}

	rules = append(rules, cacl.AclRule{
		Counter: "dataset_catch_all",
		Actions: []cacl.AclAction{
			{Kind: uint32(cacl.ActionCount)},
			{Kind: uint32(cacl.ActionDeny)},
		},
		Src4s: []filter.IPNet{filter.UnspecifiedIPv4},
		Dst4s: []filter.IPNet{filter.UnspecifiedIPv4},
		Src6s: []filter.IPNet{filter.UnspecifiedIPv6},
		Dst6s: []filter.IPNet{filter.UnspecifiedIPv6},
	})
	return rules
}

func loadDatasetPackets(path string, limit int) ([]gopacket.Packet, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var readPacket func() ([]byte, error)
	if reader, err := pcapgo.NewReader(file); err == nil {
		readPacket = func() ([]byte, error) {
			data, _, err := reader.ReadPacketData()
			return data, err
		}
	} else {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		ng, err := pcapgo.NewNgReader(file, pcapgo.DefaultNgReaderOptions)
		if err != nil {
			return nil, err
		}
		readPacket = func() ([]byte, error) {
			data, _, err := ng.ReadPacketData()
			return data, err
		}
	}

	seen := map[string]bool{}
	packets := make([]gopacket.Packet, 0, limit)
	for len(packets) < limit {
		data, err := readPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Shorter than an Ethernet header plus a minimal IP header.
		if len(data) < 34 {
			continue
		}
		copied := make([]byte, len(data))
		copy(copied, data)
		packet := gopacket.NewPacket(
			copied, layers.LayerTypeEthernet, gopacket.Default,
		)
		network := packet.NetworkLayer()
		if network == nil {
			continue
		}
		key := network.NetworkFlow().String()
		if transport := packet.TransportLayer(); transport != nil {
			key += "|" + transport.TransportFlow().String()
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		packets = append(packets, packet)
	}
	if len(packets) == 0 {
		return nil, fmt.Errorf("no usable frames in %s", path)
	}
	return packets, nil
}

// setProtocol stamps the transport protocol into either IP version.
func setProtocol(network gopacket.NetworkLayer, proto layers.IPProtocol) {
	switch header := network.(type) {
	case *layers.IPv4:
		header.Protocol = proto
	case *layers.IPv6:
		header.NextHeader = proto
	}
}

// hostInNet4 is the IPv4 counterpart of hostInNet.
func hostInNet4(network filter.IPNet, hostBits int) net.IP {
	addr := network.Addr.As4()
	mask := network.Mask.As4()
	pattern := [4]byte{
		0, byte(hostBits >> 16), byte(hostBits >> 8), byte(hostBits),
	}
	out := make(net.IP, 4)
	for idx := range out {
		out[idx] = addr[idx] | (^mask[idx] & pattern[idx])
	}
	return out
}

// hostInNet places index-derived host bits into the free (mask-zero)
// bits of a rule network, producing an address inside the rule's prefix.
func hostInNet(network filter.IPNet, hostBits int) net.IP {
	addr := network.Addr.As16()
	mask := network.Mask.As16()
	pattern := [16]byte{}
	pattern[12] = byte(hostBits >> 24)
	pattern[13] = byte(hostBits >> 16)
	pattern[14] = byte(hostBits >> 8)
	pattern[15] = byte(hostBits)
	out := make(net.IP, 16)
	for idx := range out {
		out[idx] = addr[idx] | (^mask[idx] & pattern[idx])
	}
	return out
}

// synthesizeDatasetPackets builds flow-diverse IPv6 TCP packets whose
// addresses are sampled from the ruleset's own networks.
//
// This way a batch lands in many distinct classifier classes and table
// cells the way mixed real traffic does. Uniform sampling over rules is
// a worst-case-ish cache workload; real traffic is more skewed.
func synthesizeDatasetPackets(
	b *testing.B, rules []cacl.AclRule, limit int,
) []gopacket.Packet {
	type flowSeed struct {
		src filter.IPNet
		dst filter.IPNet
		// Destination port and protocol picked from the rule when
		// constrained, so rule-specific table regions are exercised.
		dstPort uint16
		udp     bool
		ipv4    bool
	}
	seeds := make([]flowSeed, 0, limit)
	for _, rule := range rules {
		seed := flowSeed{dstPort: 443}
		switch {
		case len(rule.Src6s) > 0 && len(rule.Dst6s) > 0:
			seed.src = rule.Src6s[0]
			seed.dst = rule.Dst6s[0]
		case len(rule.Src4s) > 0 && len(rule.Dst4s) > 0:
			seed.src = rule.Src4s[0]
			seed.dst = rule.Dst4s[0]
			seed.ipv4 = true
		default:
			continue
		}
		if len(rule.DstPortRanges) > 0 {
			seed.dstPort = rule.DstPortRanges[0].From
		}
		if len(rule.ProtoRanges) > 0 &&
			rule.ProtoRanges[0] == datasetProtoUDP {
			seed.udp = true
		}
		seeds = append(seeds, seed)
	}
	require.NotEmpty(b, seeds)

	packets := make([]gopacket.Packet, 0, limit)
	for idx := 0; len(packets) < limit; idx++ {
		// Spread samples evenly over the whole seed list even when its
		// length is close to the requested packet count.
		seed := seeds[idx*len(seeds)/limit%len(seeds)]
		eth := layers.Ethernet{
			SrcMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			DstMAC:       net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
			EthernetType: layers.EthernetTypeIPv6,
		}
		var network gopacket.NetworkLayer
		if seed.ipv4 {
			eth.EthernetType = layers.EthernetTypeIPv4
			network = &layers.IPv4{
				Version:  4,
				TTL:      64,
				Protocol: layers.IPProtocolTCP,
				SrcIP:    hostInNet4(seed.src, idx),
				DstIP:    hostInNet4(seed.dst, idx*7),
			}
		} else {
			network = &layers.IPv6{
				Version:    6,
				HopLimit:   64,
				NextHeader: layers.IPProtocolTCP,
				SrcIP:      hostInNet(seed.src, idx),
				DstIP:      hostInNet(seed.dst, idx*7),
			}
		}
		var transport gopacket.SerializableLayer
		if seed.udp {
			setProtocol(network, layers.IPProtocolUDP)
			udp := &layers.UDP{
				SrcPort: layers.UDPPort(1024 + idx%60000),
				DstPort: layers.UDPPort(seed.dstPort),
			}
			require.NoError(
				b, udp.SetNetworkLayerForChecksum(network),
			)
			transport = udp
		} else {
			tcp := &layers.TCP{
				SrcPort: layers.TCPPort(1024 + idx%60000),
				DstPort: layers.TCPPort(seed.dstPort),
				ACK:     true,
				Window:  1024,
			}
			require.NoError(
				b, tcp.SetNetworkLayerForChecksum(network),
			)
			transport = tcp
		}
		packet, err := xpacket.LayersToPacketChecked(
			&eth,
			network.(gopacket.SerializableLayer),
			transport,
		)
		require.NoError(b, err)
		packets = append(packets, packet)
	}
	return packets
}

// datasetBenchState holds the lazily-built harness shared by every
// sub-benchmark in the process, so the expensive compile runs once.
type datasetBenchState struct {
	harness *dataplaneut.Harness
	agent   *ffi.Agent
	packets []gopacket.Packet
	device  string
}

var (
	datasetBenchOnce sync.Once
	datasetBench     *datasetBenchState
	datasetBenchErr  error
)

func datasetBenchSetup(b *testing.B) *datasetBenchState {
	datasetBenchOnce.Do(func() {
		var rules []cacl.AclRule
		cpMemory := 8 * datasize.GB
		agentMemory := 6 * datasize.GB
		if path := os.Getenv("ACL_DATASET_RULES"); path != "" {
			rules = loadDatasetRules(b, path)
			cpMemory = 24 * datasize.GB
			agentMemory = 16 * datasize.GB
		} else {
			count := 8192
			if v := os.Getenv("ACL_DATASET_RULE_COUNT"); v != "" {
				parsed, err := strconv.Atoi(v)
				if err != nil || parsed <= 0 {
					datasetBenchErr = fmt.Errorf(
						"invalid ACL_DATASET_RULE_COUNT %q",
						v,
					)
					return
				}
				count = parsed
			}
			rules = generateDatasetRules(count)
		}

		var packets []gopacket.Packet
		if path := os.Getenv("ACL_DATASET_PCAP"); path != "" {
			var err error
			packets, err = loadDatasetPackets(path, 4096)
			if err != nil {
				datasetBenchErr = err
				return
			}
		} else {
			packets = synthesizeDatasetPackets(b, rules, 4096)
		}

		// Register the harness device under the device name the rules
		// reference most often, so device-scoped rules from real dumps
		// stay matchable instead of falling to the catch-all.
		device := "port0"
		nameHits := map[string]int{}
		for _, rule := range rules {
			for _, ruleDevice := range rule.Devices {
				nameHits[ruleDevice.Name]++
				if nameHits[ruleDevice.Name] > nameHits[device] {
					device = ruleDevice.Name
				}
			}
		}

		cfg := dataplaneut.Config{
			CPMemory:      uint64(cpMemory),
			DPMemory:      uint64(64 * datasize.MB),
			WorkerCount:   1,
			Devices:       []string{device},
			Modules:       []string{"acl"},
			DevicesToLoad: []string{"plain"},
		}
		harness, err := dataplaneut.NewHarness(cfg)
		if err != nil {
			datasetBenchErr = err
			return
		}
		agent, err := harness.SharedMemory().AgentAttach(
			"acl-dataset-bench", 0, agentMemory,
		)
		if err != nil {
			datasetBenchErr = err
			return
		}
		backend := acl.NewBackend(agent, uint64(agentMemory))

		handle, err := backend.NewModule("dataset")
		if err != nil {
			datasetBenchErr = err
			return
		}
		if datasetBenchErr = handle.UpdateRules(rules); datasetBenchErr != nil {
			return
		}
		if datasetBenchErr = backend.UpdateModule(handle); datasetBenchErr != nil {
			return
		}
		datasetBench = &datasetBenchState{
			harness: harness,
			agent:   agent,
			packets: packets,
			device:  device,
		}
	})
	require.NoError(b, datasetBenchErr)
	// A require failure inside the once-guarded setup marks it done
	// without recording an error, so later runs must not proceed on a
	// half-built state.
	require.NotNil(
		b, datasetBench, "dataset setup failed in an earlier run",
	)
	return datasetBench
}

// BenchmarkACLDataset drives flow-diverse traffic through the ACL module
// compiled from a production-shaped (or user-supplied) ruleset.
func BenchmarkACLDataset(b *testing.B) {
	state := datasetBenchSetup(b)
	b.Logf("distinct flows loaded: %d", len(state.packets))

	wireACLPipeline(b, state.agent, state.device, "dataset")

	for _, batchSize := range []int{32, 512, 4096} {
		if batchSize > len(state.packets) {
			continue
		}
		b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
			state.harness.Bench(b, state.packets[:batchSize]...)
		})
	}
}
