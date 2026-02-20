package balancer_test

// TestFilterReuse is a comprehensive test that verifies the balancer's filter reuse logic
// during configuration updates. It tests various scenarios with 20 IPv4 and 20 IPv6 virtual
// services to ensure that:
//
// 1. IPv4 VS matcher is reused when the set of IPv4 virtual services remains the same
// 2. IPv6 VS matcher is reused when the set of IPv6 virtual services remains the same
// 3. ACL filters are reused when allowed_srcs configuration remains the same
// 4. ACL comparison is order-independent (different order = same ACL)
// 5. ACL comparison is duplicate-tolerant (duplicates don't affect equality)
//
// Test Phases:
// - Phase 1: Initial configuration with 20 IPv4 + 20 IPv6 VS
// - Phase 2: Same IPv4 set, different IPv6 set
// - Phase 3: Different IPv4 set, same IPv6 set
// - Phase 4: Same VS sets, different ACL for some VS
// - Phase 5: Same VS sets, same ACL with different order
// - Phase 6: Same VS sets, same ACL with duplicates
// - Phase 7: Completely different configuration
// - Phase 8: Identical configuration (everything reused)
// - Phase 9: Edge cases (empty ACL, mixed protocols, partial changes)

import (
	"fmt"
	"math/rand"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test constants for filter reuse tests
const (
	frIPv4VSCount = 20
	frIPv6VSCount = 20
	frRealsPerVS  = 2
)

// frGenerateIPv4Addr generates a deterministic IPv4 address based on index
func frGenerateIPv4Addr(index int) netip.Addr {
	// Generate addresses in 10.0.x.x range
	return netip.AddrFrom4([4]byte{10, 0, byte(index / 256), byte(index % 256)})
}

// frGenerateIPv6Addr generates a deterministic IPv6 address based on index
func frGenerateIPv6Addr(index int) netip.Addr {
	// Generate addresses in 2001:db8::/32 range
	return netip.AddrFrom16([16]byte{
		0x20, 0x01, 0x0d, 0xb8,
		byte(index >> 24), byte(index >> 16), byte(index >> 8), byte(index),
		0, 0, 0, 0, 0, 0, 0, 1,
	})
}

// frGenerateClientIPv4Addr generates a client IPv4 address that matches ACL rules
// For simple ACL (0.0.0.0/0), any address works
// For complex ACL, we use 10.0.x.x range which matches the 10.0.0.0/8 rule
func frGenerateClientIPv4Addr(index int) netip.Addr {
	// Generate addresses in 10.0.x.x range to match complex ACL rule (10.0.0.0/8)
	return netip.AddrFrom4(
		[4]byte{10, 0, byte((index / 256) % 256), byte(index % 256)},
	)
}

// frGenerateClientIPv6Addr generates a client IPv6 address that matches ACL rules
// For simple ACL (::/0), any address works
// For complex ACL, we use 2001:db8:1::/48 range which matches the ACL rule
func frGenerateClientIPv6Addr(index int) netip.Addr {
	// Generate addresses in 2001:db8:1::/48 range to match complex ACL rule
	return netip.AddrFrom16([16]byte{
		0x20, 0x01, 0x0d, 0xb8,
		0x00, 0x01, // 2001:db8:1::
		byte(index >> 8), byte(index),
		0, 0, 0, 0, 0, 0, 0, 1,
	})
}

// frGetVSProtocol returns the protocol for a VS at the given index
// This matches the logic in frCreateVSSet
func frGetVSProtocol(index int) balancerpb.TransportProto {
	if index%4 == 0 {
		return balancerpb.TransportProto_UDP
	}
	return balancerpb.TransportProto_TCP
}

// frGenerateRealIPv4Addr generates a deterministic IPv4 address for real servers
func frGenerateRealIPv4Addr(vsIndex, realIndex int) netip.Addr {
	// Generate addresses in 192.168.x.x range
	return netip.AddrFrom4([4]byte{192, 168, byte(vsIndex), byte(realIndex)})
}

// frGenerateRealIPv6Addr generates a deterministic IPv6 address for real servers
func frGenerateRealIPv6Addr(vsIndex, realIndex int) netip.Addr {
	// Generate addresses in fd00::/8 range
	return netip.AddrFrom16([16]byte{
		0xfd, 0x00, 0, 0,
		byte(vsIndex >> 8), byte(vsIndex), byte(realIndex), 0,
		0, 0, 0, 0, 0, 0, 0, 1,
	})
}

// frCreateReal creates a real server configuration
func frCreateReal(ip netip.Addr, weight uint32) *balancerpb.Real {
	var srcAddr, srcMask netip.Addr
	if ip.Is4() {
		srcAddr = netip.AddrFrom4([4]byte{172, 16, 0, 1})
		srcMask = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	} else {
		srcAddr = netip.AddrFrom16([16]byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
		srcMask = netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	}

	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port: 0,
		},
		Weight: weight,
		SrcAddr: &balancerpb.Addr{
			Bytes: srcAddr.AsSlice(),
		},
		SrcMask: &balancerpb.Addr{
			Bytes: srcMask.AsSlice(),
		},
	}
}

// frCreateSimpleACL creates a simple allow-all ACL
func frCreateSimpleACL(isIPv6 bool) []*balancerpb.AllowedSrc {
	var addr, mask netip.Addr
	if isIPv6 {
		addr = netip.AddrFrom16([16]byte{})
		mask = netip.AddrFrom16([16]byte{})
	} else {
		addr = netip.AddrFrom4([4]byte{0, 0, 0, 0})
		mask = netip.AddrFrom4([4]byte{0, 0, 0, 0})
	}

	return []*balancerpb.AllowedSrc{
		{
			Net: &balancerpb.Net{
				Addr: &balancerpb.Addr{Bytes: addr.AsSlice()},
				Mask: &balancerpb.Addr{Bytes: mask.AsSlice()},
			},
		},
	}
}

// frCreateComplexACL creates a complex ACL with multiple rules and port ranges
func frCreateComplexACL(index int, isIPv6 bool) []*balancerpb.AllowedSrc {
	var acl []*balancerpb.AllowedSrc

	if isIPv6 {
		// Rule 1: Allow from 2001:db8:1::/48
		acl = append(acl, &balancerpb.AllowedSrc{
			Net: &balancerpb.Net{
				Addr: &balancerpb.Addr{
					Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
						AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
						AsSlice(),
				},
			},
			Ports: []*balancerpb.PortsRange{
				{From: 1024, To: 65535},
			},
		})

		// Rule 2: Allow from 2001:db8:2::/48 with specific ports
		acl = append(acl, &balancerpb.AllowedSrc{
			Net: &balancerpb.Net{
				Addr: &balancerpb.Addr{
					Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
						AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
						AsSlice(),
				},
			},
			Ports: []*balancerpb.PortsRange{
				{From: 80, To: 80},
				{From: 443, To: 443},
			},
		})
	} else {
		// Rule 1: Allow from 10.0.0.0/8
		acl = append(acl, &balancerpb.AllowedSrc{
			Net: &balancerpb.Net{
				Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{10, 0, 0, 0}).AsSlice()},
				Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 0, 0, 0}).AsSlice()},
			},
			Ports: []*balancerpb.PortsRange{
				{From: 1024, To: 65535},
			},
		})

		// Rule 2: Allow from 192.168.0.0/16 with specific ports
		acl = append(acl, &balancerpb.AllowedSrc{
			Net: &balancerpb.Net{
				Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{192, 168, 0, 0}).AsSlice()},
				Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice()},
			},
			Ports: []*balancerpb.PortsRange{
				{From: 80, To: 80},
				{From: 443, To: 443},
			},
		})
	}

	// Add index-specific rule for variation
	if index%2 == 0 {
		if isIPv6 {
			acl = append(acl, &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
				},
			})
		} else {
			acl = append(acl, &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{172, 16, 0, 0}).AsSlice()},
					Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice()},
				},
			})
		}
	}

	return acl
}

// frShuffleACL returns a new ACL with rules in random order
func frShuffleACL(
	acl []*balancerpb.AllowedSrc,
	rng *rand.Rand,
) []*balancerpb.AllowedSrc {
	shuffled := make([]*balancerpb.AllowedSrc, len(acl))
	copy(shuffled, acl)
	rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return shuffled
}

// frDuplicateACLRules returns a new ACL with some rules duplicated
func frDuplicateACLRules(
	acl []*balancerpb.AllowedSrc,
) []*balancerpb.AllowedSrc {
	if len(acl) == 0 {
		return acl
	}

	duplicated := make([]*balancerpb.AllowedSrc, 0, len(acl)*2)
	for i, rule := range acl {
		duplicated = append(duplicated, rule)
		// Duplicate every other rule
		if i%2 == 0 {
			duplicated = append(duplicated, rule)
		}
	}
	return duplicated
}

// frCreateVirtualService creates a virtual service with given parameters
func frCreateVirtualService(
	ip netip.Addr,
	port uint16,
	proto balancerpb.TransportProto,
	reals []*balancerpb.Real,
	acl []*balancerpb.AllowedSrc,
	scheduler balancerpb.VsScheduler,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port:  uint32(port),
			Proto: proto,
		},
		Scheduler:   scheduler,
		AllowedSrcs: acl,
		Reals:       reals,
		Flags: &balancerpb.VsFlags{
			Gre:    false,
			FixMss: false,
			Ops:    false,
			PureL3: false,
			Wlc:    false,
		},
		Peers: []*balancerpb.Addr{},
	}
}

// frCreateVSSet creates a set of virtual services (IPv4 or IPv6)
func frCreateVSSet(
	count int,
	isIPv6 bool,
	baseIndex int,
	useComplexACL bool,
) []*balancerpb.VirtualService {
	vsList := make([]*balancerpb.VirtualService, 0, count)

	for i := 0; i < count; i++ {
		var vsIP netip.Addr
		if isIPv6 {
			vsIP = frGenerateIPv6Addr(baseIndex + i)
		} else {
			vsIP = frGenerateIPv4Addr(baseIndex + i)
		}

		// Create reals for this VS
		reals := make([]*balancerpb.Real, 0, frRealsPerVS)
		for j := 0; j < frRealsPerVS; j++ {
			var realIP netip.Addr
			if isIPv6 {
				realIP = frGenerateRealIPv6Addr(baseIndex+i, j)
			} else {
				realIP = frGenerateRealIPv4Addr(baseIndex+i, j)
			}
			reals = append(reals, frCreateReal(realIP, 1))
		}

		// Create ACL
		var acl []*balancerpb.AllowedSrc
		if useComplexACL && i%3 == 0 {
			// Use complex ACL for every 3rd VS
			acl = frCreateComplexACL(i, isIPv6)
		} else {
			// Use simple ACL for others
			acl = frCreateSimpleACL(isIPv6)
		}

		// Alternate between TCP and UDP
		proto := balancerpb.TransportProto_TCP
		if i%4 == 0 {
			proto = balancerpb.TransportProto_UDP
		}

		// Alternate between schedulers
		scheduler := balancerpb.VsScheduler_ROUND_ROBIN
		if i%2 == 0 {
			scheduler = balancerpb.VsScheduler_SOURCE_HASH
		}

		vs := frCreateVirtualService(vsIP, 80, proto, reals, acl, scheduler)
		vsList = append(vsList, vs)
	}

	return vsList
}

// frCreateConfig creates a balancer configuration with given VS sets
func frCreateConfig(
	ipv4VS, ipv6VS []*balancerpb.VirtualService,
) *balancerpb.BalancerConfig {
	allVS := make([]*balancerpb.VirtualService, 0, len(ipv4VS)+len(ipv6VS))
	allVS = append(allVS, ipv4VS...)
	allVS = append(allVS, ipv6VS...)

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs:             allVS,
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
				Default:   60,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(10000); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

// frVerifyUpdateInfo verifies the UpdateInfo fields match expectations
func frVerifyUpdateInfo(
	t *testing.T,
	updateInfo *ffi.UpdateInfo,
	expectedIPv4Reused bool,
	expectedIPv6Reused bool,
	expectedACLReusedCount int,
) {
	t.Helper()

	assert.Equal(t, expectedIPv4Reused, updateInfo.VsIpv4MatcherReused,
		"IPv4 VS matcher reuse mismatch")
	assert.Equal(t, expectedIPv6Reused, updateInfo.VsIpv6MatcherReused,
		"IPv6 VS matcher reuse mismatch")
	assert.Equal(t, expectedACLReusedCount, len(updateInfo.ACLReusedVs),
		"ACL reused count mismatch")
}

// frVerifyACLReusedVS verifies that specific VS indices have ACL reused
// expectedIndices should be a map of VS index to expected reuse status
func frVerifyACLReusedVS(
	t *testing.T,
	updateInfo *ffi.UpdateInfo,
	vsList []*balancerpb.VirtualService,
	expectedIndices map[int]bool,
) {
	t.Helper()

	// Build a map of VS identifiers that have ACL reused
	reusedVSMap := make(map[string]bool)
	for _, vsID := range updateInfo.ACLReusedVs {
		addr, _ := netip.AddrFromSlice(vsID.Addr.AsSlice())
		key := fmt.Sprintf("%s:%d/%d", addr, vsID.Port, vsID.TransportProto)
		reusedVSMap[key] = true
	}

	// Check each expected index
	for idx, shouldBeReused := range expectedIndices {
		if idx >= len(vsList) {
			t.Errorf(
				"Index %d out of range (VS list has %d elements)",
				idx,
				len(vsList),
			)
			continue
		}

		vs := vsList[idx]
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		key := fmt.Sprintf("%s:%d/%d", addr, vs.Id.Port, vs.Id.Proto)

		isReused := reusedVSMap[key]
		if shouldBeReused && !isReused {
			t.Errorf(
				"VS at index %d (%s) should have ACL reused but doesn't",
				idx,
				key,
			)
		} else if !shouldBeReused && isReused {
			t.Errorf("VS at index %d (%s) should NOT have ACL reused but does", idx, key)
		}
	}
}

// frSendTestPackets sends test packets to a VS and verifies they are processed
func frSendTestPackets(
	t *testing.T,
	ts *utils.TestSetup,
	vsIP netip.Addr,
	vsPort uint16,
	proto balancerpb.TransportProto,
	count int,
	clientBaseIndex int,
) {
	t.Helper()

	for i := range count {
		var clientIP netip.Addr
		if vsIP.Is4() {
			// Use client IPs that match ACL rules (10.0.0.0/8 for complex ACL)
			clientIP = frGenerateClientIPv4Addr(1000 + clientBaseIndex + i)
		} else {
			// Use client IPs that match ACL rules (2001:db8:1::/48 for complex ACL)
			clientIP = frGenerateClientIPv6Addr(1000 + clientBaseIndex + i)
		}
		clientPort := uint16(10000 + i)

		var packetLayers []gopacket.SerializableLayer
		if proto == balancerpb.TransportProto_TCP {
			packetLayers = utils.MakeTCPPacket(
				clientIP,
				clientPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
		} else {
			packetLayers = utils.MakeUDPPacket(
				clientIP,
				clientPort,
				vsIP,
				vsPort,
			)
		}

		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"expected 1 output packet for VS %s",
			vsIP,
		)
		require.Empty(
			t,
			result.Drop,
			"expected no dropped packets for VS %s",
			vsIP,
		)
	}
}

// TestFilterReuse is the main test function
func TestFilterReuse(t *testing.T) {
	// Setup test with appropriate memory
	agentMemory := 512 * datasize.MB

	// Create initial configuration
	ipv4VS := frCreateVSSet(frIPv4VSCount, false, 0, true)
	ipv6VS := frCreateVSSet(frIPv6VSCount, true, 0, true)
	initialConfig := frCreateConfig(ipv4VS, ipv6VS)

	ts, err := utils.Make(&utils.TestConfig{
		Mock: utils.SingleWorkerMockConfig(
			1024*datasize.MB,
			16*datasize.MB,
		),
		Balancer:    initialConfig,
		AgentMemory: &agentMemory,
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Logf(
		"Setup initial config with %d IPv4 and %d IPv6 virtual services",
		frIPv4VSCount,
		frIPv6VSCount,
	)

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Phase 1: Verify initial setup works
	t.Run("Phase1_InitialSetup", func(t *testing.T) {
		t.Log("Verifying initial configuration is accessible")

		// Test a few IPv4 VS
		for i := 0; i < 3; i++ {
			vsIP := frGenerateIPv4Addr(i)
			proto := balancerpb.TransportProto_TCP
			if i%4 == 0 {
				proto = balancerpb.TransportProto_UDP
			}
			frSendTestPackets(t, ts, vsIP, 80, proto, 2, i*100)
		}

		// Test a few IPv6 VS
		for i := 0; i < 3; i++ {
			vsIP := frGenerateIPv6Addr(i)
			proto := balancerpb.TransportProto_TCP
			if i%4 == 0 {
				proto = balancerpb.TransportProto_UDP
			}
			frSendTestPackets(t, ts, vsIP, 80, proto, 2, i*100+1000)
		}

		t.Log("Initial configuration verified successfully")
	})

	// Phase 2: Same IPv4 VS set, Different IPv6 VS set
	t.Run("Phase2_SameIPv4_DifferentIPv6", func(t *testing.T) {
		t.Log("Testing: Same IPv4 set, different IPv6 set")

		// Keep IPv4 VS the same
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 0, true)

		// Create different IPv6 VS (different base index)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 1000, true)

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Verify: IPv4 matcher reused, IPv6 matcher NOT reused
		frVerifyUpdateInfo(t, updateInfo, true, false, frIPv4VSCount)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Enable new reals and test
		utils.EnableAllReals(t, ts)
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(1),
			80,
			balancerpb.TransportProto_TCP,
			2,
			2000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(1001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			2100,
		)
	})

	// Phase 3: Different IPv4 VS set, Same IPv6 VS set
	t.Run("Phase3_DifferentIPv4_SameIPv6", func(t *testing.T) {
		t.Log("Testing: Different IPv4 set, same IPv6 set")

		// Create different IPv4 VS (different base index)
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 2000, true)

		// Keep IPv6 VS the same as Phase 2
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 1000, true)

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Verify: IPv4 matcher NOT reused, IPv6 matcher reused
		frVerifyUpdateInfo(t, updateInfo, false, true, frIPv6VSCount)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Enable new reals and test
		utils.EnableAllReals(t, ts)
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(2001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			3000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(1001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			3100,
		)
	})

	// Phase 4: Same VS sets, Different ACL for some VS
	t.Run("Phase4_SameVS_DifferentACLForSome", func(t *testing.T) {
		t.Log("Testing: Same VS sets, different ACL for some VS")

		// Keep VS identifiers the same as Phase 3
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 2000, true)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 1000, true)

		// Change ACL for 5 IPv4 VS and 5 IPv6 VS by adding a unique rule
		// This ensures the ACL is truly different from the original
		for i := 0; i < 5; i++ {
			// Add a unique network rule that makes this ACL different
			uniqueRule := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{100, byte(i), 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv4VS[i].AllowedSrcs = append(
				newIPv4VS[i].AllowedSrcs,
				uniqueRule,
			)

			uniqueRuleV6 := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0x00, byte(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv6VS[i].AllowedSrcs = append(
				newIPv6VS[i].AllowedSrcs,
				uniqueRuleV6,
			)
		}

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Expected behavior:
		// - Both matchers reused (VS identifiers unchanged)
		// - 30 VS have ACL reused: 15 unchanged IPv4 + 15 unchanged IPv6
		// - 10 VS have different ACL: 5 modified IPv4 + 5 modified IPv6
		assert.True(
			t,
			updateInfo.VsIpv4MatcherReused,
			"IPv4 matcher should be reused",
		)
		assert.True(
			t,
			updateInfo.VsIpv6MatcherReused,
			"IPv6 matcher should be reused",
		)
		assert.Equal(
			t,
			30,
			len(updateInfo.ACLReusedVs),
			"30 VS should have ACL reused (15 unchanged IPv4 + 15 unchanged IPv6)",
		)

		// Verify specific VS indices have correct ACL reuse status
		allVS := append(newIPv4VS, newIPv6VS...)
		expectedReuse := make(map[int]bool)
		// First 5 IPv4 VS (indices 0-4) have modified ACL - should NOT be reused
		for i := range 5 {
			expectedReuse[i] = false
		}
		// Remaining 15 IPv4 VS (indices 5-19) have unchanged ACL - should be reused
		for i := 5; i < 20; i++ {
			expectedReuse[i] = true
		}
		// First 5 IPv6 VS (indices 20-24) have modified ACL - should NOT be reused
		for i := 20; i < 25; i++ {
			expectedReuse[i] = false
		}
		// Remaining 15 IPv6 VS (indices 25-39) have unchanged ACL - should be reused
		for i := 25; i < 40; i++ {
			expectedReuse[i] = true
		}
		frVerifyACLReusedVS(t, updateInfo, allVS, expectedReuse)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Test packet processing still works
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(2001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			4000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(1001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			4100,
		)
	})

	// Phase 5: Same VS sets, Same ACL with different order
	t.Run("Phase5_SameVS_SameACL_DifferentOrder", func(t *testing.T) {
		t.Log("Testing: Same VS sets, same ACL with different order")

		rng := rand.New(rand.NewSource(42))

		// Keep VS identifiers the same as Phase 4
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 2000, true)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 1000, true)

		// Apply the SAME ACL changes as Phase 4 (add unique rules to first 5 VS)
		for i := range 5 {
			uniqueRule := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{100, byte(i), 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv4VS[i].AllowedSrcs = append(
				newIPv4VS[i].AllowedSrcs,
				uniqueRule,
			)

			uniqueRuleV6 := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0x00, byte(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv6VS[i].AllowedSrcs = append(
				newIPv6VS[i].AllowedSrcs,
				uniqueRuleV6,
			)
		}

		// Shuffle ACL order for all VS - this should NOT affect ACL equality
		for i := range newIPv4VS {
			newIPv4VS[i].AllowedSrcs = frShuffleACL(
				newIPv4VS[i].AllowedSrcs,
				rng,
			)
		}
		for i := range newIPv6VS {
			newIPv6VS[i].AllowedSrcs = frShuffleACL(
				newIPv6VS[i].AllowedSrcs,
				rng,
			)
		}

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Expected behavior:
		// - Both matchers reused (VS identifiers unchanged)
		// - ALL VS have ACL reused because order doesn't matter for ACL comparison
		assert.True(
			t,
			updateInfo.VsIpv4MatcherReused,
			"IPv4 matcher should be reused",
		)
		assert.True(
			t,
			updateInfo.VsIpv6MatcherReused,
			"IPv6 matcher should be reused",
		)
		assert.Equal(
			t,
			frIPv4VSCount+frIPv6VSCount,
			len(updateInfo.ACLReusedVs),
			"All 40 VS should have ACL reused (order doesn't matter)",
		)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Test packet processing still works
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(2001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			5000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(1001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			5100,
		)
	})

	// Phase 6: Same VS sets, Same ACL with duplicates
	t.Run("Phase6_SameVS_SameACL_WithDuplicates", func(t *testing.T) {
		t.Log("Testing: Same VS sets, same ACL with duplicates")

		rng := rand.New(rand.NewSource(42))

		// Keep VS identifiers the same as Phase 5
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 2000, true)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 1000, true)

		// Apply the SAME ACL changes as Phase 4/5 (add unique rules to first 5 VS)
		for i := 0; i < 5; i++ {
			uniqueRule := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{100, byte(i), 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv4VS[i].AllowedSrcs = append(
				newIPv4VS[i].AllowedSrcs,
				uniqueRule,
			)

			uniqueRuleV6 := &balancerpb.AllowedSrc{
				Net: &balancerpb.Net{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0x00, byte(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).
							AsSlice(),
					},
				},
			}
			newIPv6VS[i].AllowedSrcs = append(
				newIPv6VS[i].AllowedSrcs,
				uniqueRuleV6,
			)
		}

		// Shuffle ACL order (same as Phase 5)
		for i := range newIPv4VS {
			newIPv4VS[i].AllowedSrcs = frShuffleACL(
				newIPv4VS[i].AllowedSrcs,
				rng,
			)
		}
		for i := range newIPv6VS {
			newIPv6VS[i].AllowedSrcs = frShuffleACL(
				newIPv6VS[i].AllowedSrcs,
				rng,
			)
		}

		// Add duplicates to ACL - this should NOT affect ACL equality
		for i := range newIPv4VS {
			newIPv4VS[i].AllowedSrcs = frDuplicateACLRules(
				newIPv4VS[i].AllowedSrcs,
			)
		}
		for i := range newIPv6VS {
			newIPv6VS[i].AllowedSrcs = frDuplicateACLRules(
				newIPv6VS[i].AllowedSrcs,
			)
		}

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Expected behavior:
		// - Both matchers reused (VS identifiers unchanged)
		// - ALL VS have ACL reused because duplicates don't matter for ACL comparison
		assert.True(
			t,
			updateInfo.VsIpv4MatcherReused,
			"IPv4 matcher should be reused",
		)
		assert.True(
			t,
			updateInfo.VsIpv6MatcherReused,
			"IPv6 matcher should be reused",
		)
		assert.Equal(
			t,
			frIPv4VSCount+frIPv6VSCount,
			len(updateInfo.ACLReusedVs),
			"All 40 VS should have ACL reused (duplicates don't matter)",
		)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Test packet processing still works
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(2001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			6000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(1001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			6100,
		)
	})

	// Phase 7: Completely different configuration
	t.Run("Phase7_CompletelyDifferent", func(t *testing.T) {
		t.Log("Testing: Completely different configuration")

		// Create completely new VS sets with different base indices
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Verify: Nothing reused
		frVerifyUpdateInfo(t, updateInfo, false, false, 0)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Enable new reals and test
		utils.EnableAllReals(t, ts)
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(5001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			7000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(5001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			7100,
		)
	})

	// Phase 8: Identical configuration (everything reused)
	t.Run("Phase8_IdenticalConfig", func(t *testing.T) {
		t.Log("Testing: Identical configuration (everything should be reused)")

		// Create the exact same configuration as Phase 7
		newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
		newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)

		newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
		updateInfo, err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		// Verify: Everything reused
		frVerifyUpdateInfo(
			t,
			updateInfo,
			true,
			true,
			frIPv4VSCount+frIPv6VSCount,
		)

		t.Logf(
			"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
			updateInfo.VsIpv4MatcherReused,
			updateInfo.VsIpv6MatcherReused,
			len(updateInfo.ACLReusedVs),
		)

		// Test packet processing still works
		// Use VS index 1 which has TCP protocol (index 0 has UDP because 0%4==0)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv4Addr(5001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			8000,
		)
		frSendTestPackets(
			t,
			ts,
			frGenerateIPv6Addr(5001),
			80,
			balancerpb.TransportProto_TCP,
			2,
			8100,
		)
	})

	// Phase 9: Edge cases
	// Each subtest explicitly sets up a known state first, then makes a specific change
	// to test the expected reuse behavior.
	t.Run("Phase9_EdgeCases", func(t *testing.T) {
		// Phase 9a: Partial VS set changes (add some VS, remove some VS)
		t.Run("PartialVSChanges", func(t *testing.T) {
			t.Log("Testing: Partial VS set changes")

			// First, establish a known baseline state
			baseIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			baseIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)
			baseConfig := frCreateConfig(baseIPv4VS, baseIPv6VS)
			_, err := ts.Balancer.Update(baseConfig, ts.Mock.CurrentTime())
			require.NoError(t, err)
			utils.EnableAllReals(t, ts)
			t.Log(
				"Baseline established: 20 IPv4 VS (base 5000) + 20 IPv6 VS (base 5000)",
			)

			// Now make partial changes: keep first 15 VS, add 5 new ones
			newIPv4VS := frCreateVSSet(15, false, 5000, true)
			additionalIPv4VS := frCreateVSSet(5, false, 6000, true)
			newIPv4VS = append(newIPv4VS, additionalIPv4VS...)

			newIPv6VS := frCreateVSSet(15, true, 5000, true)
			additionalIPv6VS := frCreateVSSet(5, true, 6000, true)
			newIPv6VS = append(newIPv6VS, additionalIPv6VS...)

			newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
			updateInfo, err := ts.Balancer.Update(
				newConfig,
				ts.Mock.CurrentTime(),
			)
			require.NoError(t, err)

			// Expected behavior:
			// - VS matchers NOT reused because the VS set changed (removed 5, added 5 different)
			// - ACL reused for the 15 unchanged VS in each family = 30 total
			assert.False(
				t,
				updateInfo.VsIpv4MatcherReused,
				"IPv4 matcher should NOT be reused when VS set changes (removed 5 VS, added 5 new)",
			)
			assert.False(
				t,
				updateInfo.VsIpv6MatcherReused,
				"IPv6 matcher should NOT be reused when VS set changes (removed 5 VS, added 5 new)",
			)
			assert.Equal(
				t,
				30,
				len(updateInfo.ACLReusedVs),
				"30 VS should have ACL reused (15 unchanged IPv4 + 15 unchanged IPv6)",
			)

			t.Logf(
				"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
				updateInfo.VsIpv4MatcherReused,
				updateInfo.VsIpv6MatcherReused,
				len(updateInfo.ACLReusedVs),
			)

			utils.EnableAllReals(t, ts)
		})

		// Phase 9b: Mixed protocol changes - change protocol for some IPv4 VS only
		t.Run("MixedProtocolChanges", func(t *testing.T) {
			t.Log("Testing: Mixed protocol changes")

			// First, establish a known baseline state
			baseIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			baseIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)
			baseConfig := frCreateConfig(baseIPv4VS, baseIPv6VS)
			_, err := ts.Balancer.Update(baseConfig, ts.Mock.CurrentTime())
			require.NoError(t, err)
			utils.EnableAllReals(t, ts)
			t.Log(
				"Baseline established: 20 IPv4 VS + 20 IPv6 VS with standard protocols",
			)

			// Now change protocol for first 5 IPv4 VS
			newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)

			// Change protocol for first 5 IPv4 VS from TCP to UDP (or vice versa)
			for i := 0; i < 5; i++ {
				if newIPv4VS[i].Id.Proto == balancerpb.TransportProto_TCP {
					newIPv4VS[i].Id.Proto = balancerpb.TransportProto_UDP
				} else {
					newIPv4VS[i].Id.Proto = balancerpb.TransportProto_TCP
				}
			}

			newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
			updateInfo, err := ts.Balancer.Update(
				newConfig,
				ts.Mock.CurrentTime(),
			)
			require.NoError(t, err)

			// Expected behavior:
			// - IPv4 matcher NOT reused because protocol changed for 5 VS (different VS identifiers)
			// - IPv6 matcher REUSED because IPv6 VS set is identical
			// - ACL reused for unchanged VS: 15 IPv4 (with same identifier) + 20 IPv6 = 35
			assert.False(
				t,
				updateInfo.VsIpv4MatcherReused,
				"IPv4 matcher should NOT be reused when protocol changes for some VS",
			)
			assert.True(t, updateInfo.VsIpv6MatcherReused,
				"IPv6 matcher should be reused (IPv6 VS set unchanged)")

			t.Logf(
				"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
				updateInfo.VsIpv4MatcherReused,
				updateInfo.VsIpv6MatcherReused,
				len(updateInfo.ACLReusedVs),
			)

			utils.EnableAllReals(t, ts)
		})

		// Phase 9c: Port changes - change port for some IPv4 VS only
		t.Run("PortChanges", func(t *testing.T) {
			t.Log("Testing: Port changes")

			// First, establish a known baseline state
			baseIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			baseIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)
			baseConfig := frCreateConfig(baseIPv4VS, baseIPv6VS)
			_, err := ts.Balancer.Update(baseConfig, ts.Mock.CurrentTime())
			require.NoError(t, err)
			utils.EnableAllReals(t, ts)
			t.Log("Baseline established: 20 IPv4 VS + 20 IPv6 VS with port 80")

			// Now change port for first 5 IPv4 VS
			newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)

			for i := range 5 {
				newIPv4VS[i].Id.Port = 8080
			}

			newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
			updateInfo, err := ts.Balancer.Update(
				newConfig,
				ts.Mock.CurrentTime(),
			)
			require.NoError(t, err)

			// Expected behavior:
			// - IPv4 matcher NOT reused because port changed for 5 VS (different VS identifiers)
			// - IPv6 matcher REUSED because IPv6 VS set is identical
			assert.False(
				t,
				updateInfo.VsIpv4MatcherReused,
				"IPv4 matcher should NOT be reused when port changes for some VS",
			)
			assert.True(t, updateInfo.VsIpv6MatcherReused,
				"IPv6 matcher should be reused (IPv6 VS set unchanged)")

			t.Logf(
				"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
				updateInfo.VsIpv4MatcherReused,
				updateInfo.VsIpv6MatcherReused,
				len(updateInfo.ACLReusedVs),
			)

			utils.EnableAllReals(t, ts)
		})

		// Phase 9d: ACL with different port ranges (same VS identifiers, different ACL)
		t.Run("ACLPortRangeChanges", func(t *testing.T) {
			t.Log("Testing: ACL with different port ranges")

			// First, establish a known baseline state
			baseIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			baseIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)
			baseConfig := frCreateConfig(baseIPv4VS, baseIPv6VS)
			_, err := ts.Balancer.Update(baseConfig, ts.Mock.CurrentTime())
			require.NoError(t, err)
			utils.EnableAllReals(t, ts)
			t.Log(
				"Baseline established: 20 IPv4 VS + 20 IPv6 VS with standard ACLs",
			)

			// Now change ACL port ranges for first 5 IPv4 VS
			newIPv4VS := frCreateVSSet(frIPv4VSCount, false, 5000, true)
			newIPv6VS := frCreateVSSet(frIPv6VSCount, true, 5000, true)

			// Change port ranges in ACL for first 5 IPv4 VS
			for i := 0; i < 5; i++ {
				if len(newIPv4VS[i].AllowedSrcs) > 0 {
					// Add a new port range to the ACL
					newIPv4VS[i].AllowedSrcs[0].Ports = append(
						newIPv4VS[i].AllowedSrcs[0].Ports,
						&balancerpb.PortsRange{From: 8000, To: 9000},
					)
				}
			}

			newConfig := frCreateConfig(newIPv4VS, newIPv6VS)
			updateInfo, err := ts.Balancer.Update(
				newConfig,
				ts.Mock.CurrentTime(),
			)
			require.NoError(t, err)

			// Expected behavior:
			// - Both matchers REUSED because VS identifiers are identical
			// - ACL reused for 15 IPv4 (unchanged ACL) + 20 IPv6 (unchanged) = 35
			assert.True(t, updateInfo.VsIpv4MatcherReused,
				"IPv4 matcher should be reused (VS identifiers unchanged)")
			assert.True(t, updateInfo.VsIpv6MatcherReused,
				"IPv6 matcher should be reused (VS identifiers unchanged)")
			assert.Equal(
				t,
				35,
				len(updateInfo.ACLReusedVs),
				"35 VS should have ACL reused (15 unchanged IPv4 + 20 unchanged IPv6)",
			)

			t.Logf(
				"IPv4 matcher reused: %v, IPv6 matcher reused: %v, ACL reused count: %d",
				updateInfo.VsIpv4MatcherReused,
				updateInfo.VsIpv6MatcherReused,
				len(updateInfo.ACLReusedVs),
			)
		})
	})
}
