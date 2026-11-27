//go:build linux && (amd64 || arm64)

package classify_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// productionRulesets names the production-shaped ACL rulesets, JSON
// files separated by colons; the production-shaped cases skip without it.
const productionRulesets = "YANET_PRODUCTION_RULESETS"

// productionMemorySize is the size of the memory a production-shaped
// classifier is committed into.
const productionMemorySize = 4 << 30

// productionPackets is the count of packets sampled per production-shaped
// ruleset.
const productionPackets = 512

// aclDocument is the part of an ACL ruleset the ratelimit-shaped
// classifier reads.
type aclDocument struct {
	Rules []struct {
		Srcs          []string
		Dsts          []string
		ProtoRanges   []aclRange `json:"proto_ranges"`
		DstPortRanges []aclRange `json:"dst_port_ranges"`
	}
}

// aclRange is an ACL interval.
type aclRange struct {
	From uint32
	To   uint32
}

// loadProductionRulesets reads the rulesets the variable names as the
// ratelimit classifiers of both families read them, or skips the test
// without it.
//
// A rule without a network of a family on either side is absent from
// all four attributes of the family. Rules spelling one address list
// share one list of its networks, as a producer hands its lists out.
func loadProductionRulesets(t testing.TB) []ratelimitRuleset {
	t.Helper()
	paths := os.Getenv(productionRulesets)
	if paths == "" {
		t.Skipf("set %s to run the production-shaped cases", productionRulesets)
	}
	var rulesets []ratelimitRuleset
	for path := range strings.SplitSeq(paths, ":") {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var document aclDocument
		require.NoError(t, json.Unmarshal(data, &document))
		var ruleset ratelimitRuleset
		lists4 := map[string][]netip.Prefix{}
		lists6 := map[string][]classify.Net6Network{}
		for _, rule := range document.Rules {
			var protocols classify.LineRule
			// An ACL protocol interval spans the protocol in its high byte.
			for _, span := range rule.ProtoRanges {
				protocols.Ranges = append(protocols.Ranges, classify.LineRange{From: span.From >> 8, To: span.To >> 8})
			}
			var ports classify.PortRule
			for _, span := range rule.DstPortRanges {
				ports.Ranges = append(ports.Ranges, classify.PortRange{From: uint16(span.From), To: uint16(span.To)})
			}

			source4, destination4 := shared(lists4, rule.Srcs, ipv4Prefixes), shared(lists4, rule.Dsts, ipv4Prefixes)
			absent4 := len(source4) == 0 || len(destination4) == 0
			ruleset.Source4 = append(ruleset.Source4, classify.Net4Rule{Prefixes: source4, Absent: absent4})
			ruleset.Destination4 = append(ruleset.Destination4, classify.Net4Rule{Prefixes: destination4, Absent: absent4})
			ruleset.Protocol4 = append(ruleset.Protocol4, classify.LineRule{Ranges: protocols.Ranges, Absent: absent4})
			ruleset.Port4 = append(ruleset.Port4, classify.PortRule{Ranges: ports.Ranges, Absent: absent4})

			source6, destination6 := shared(lists6, rule.Srcs, ipv6Networks), shared(lists6, rule.Dsts, ipv6Networks)
			absent6 := len(source6) == 0 || len(destination6) == 0
			ruleset.Source6 = append(ruleset.Source6, classify.Net6Rule{Networks: source6, Absent: absent6})
			ruleset.Destination6 = append(ruleset.Destination6, classify.Net6Rule{Networks: destination6, Absent: absent6})
			ruleset.Protocol6 = append(ruleset.Protocol6, classify.LineRule{Ranges: protocols.Ranges, Absent: absent6})
			ruleset.Port6 = append(ruleset.Port6, classify.PortRule{Ranges: ports.Ranges, Absent: absent6})
		}
		rulesets = append(rulesets, ruleset)
	}
	return rulesets
}

// shared returns the networks of an address list, parsed once a list and
// shared by every rule spelling it.
func shared[T any](lists map[string][]T, addresses []string, parse func([]string) []T) []T {
	key := strings.Join(addresses, " ")
	list, ok := lists[key]
	if !ok {
		list = parse(addresses)
		lists[key] = list
	}
	return list
}

// ipv4Prefixes returns the IPv4 networks among addresses, a bare address
// a host network.
func ipv4Prefixes(addresses []string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, address := range addresses {
		if strings.Contains(address, ":") {
			continue
		}
		if !strings.Contains(address, "/") {
			address += "/32"
		}
		prefixes = append(prefixes, netip.MustParsePrefix(address))
	}
	return prefixes
}

// ipv6Networks returns the IPv6 networks among addresses: a prefix, a
// bare address as a host network, or an address and a mask spelled as
// an address.
func ipv6Networks(addresses []string) []classify.Net6Network {
	var networks []classify.Net6Network
	for _, address := range addresses {
		if !strings.Contains(address, ":") {
			continue
		}
		addr, mask, found := strings.Cut(address, "/")
		var network classify.Net6Network
		network.Addr = netip.MustParseAddr(addr).As16()
		switch {
		case !found:
			for idx := range network.Mask {
				network.Mask[idx] = 0xff
			}
		case strings.Contains(mask, ":"):
			network.Mask = netip.MustParseAddr(mask).As16()
		default:
			for bit := range netip.MustParsePrefix(address).Bits() {
				network.Mask[bit/8] |= 0x80 >> (bit % 8)
			}
		}
		networks = append(networks, network)
	}
	return networks
}

// Test_ProductionShaped_ResolvesFirstMatch verifies that sampled packets
// of either family classified through the ratelimit-shaped classifier of
// every production-shaped ruleset resolve to the first rule of their
// family matching them, and that freeing it gives every byte back.
func Test_ProductionShaped_ResolvesFirstMatch(t *testing.T) {
	for idx, ruleset := range loadProductionRulesets(t) {
		t.Run(fmt.Sprintf("ruleset %d", idx), func(t *testing.T) {
			memory := classifytest.Map(t, productionMemorySize)
			free := shmalloc.FreeSize(&memory.Allocator)
			holder := commitRatelimitShaped(t, memory, ruleset)

			classifier, keys := readRatelimitShaped(holder), newRatelimitKeys(ruleset)
			random := rand.New(rand.NewPCG(uint64(idx), 31))
			pick := func(_ string, count int) int { return random.IntN(count) }
			for packetIdx := range productionPackets {
				packet := keys.Packet(pick)
				// The message names the packet by its number alone: its
				// fields come from the ruleset.
				require.Equal(t, ruleset.FirstMatch(packet), classifier.Classify(packet), "the rule of packet %d", packetIdx)
			}
			freeRatelimitShaped(memory, holder)
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
		})
	}
}

// Benchmark_ProductionShaped measures compiling, committing and freeing
// the ratelimit-shaped classifier of every production-shaped ruleset.
func Benchmark_ProductionShaped(b *testing.B) {
	for idx, ruleset := range loadProductionRulesets(b) {
		b.Run(fmt.Sprintf("ruleset %d", idx), func(b *testing.B) {
			memory := classifytest.Map(b, productionMemorySize)
			for b.Loop() {
				freeRatelimitShaped(memory, commitRatelimitShaped(b, memory, ruleset))
			}
		})
	}
}
