package balancer

import (
	"math/rand/v2"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestACLAndFilterReuse is a comprehensive test that verifies the balancer's ACL and filter
// reuse optimization during configuration updates. It analyzes UpdateInfo returned by Update()
// to verify that:
//
// 1. IPv4 VS matcher is reused when the set of IPv4 virtual services remains unchanged
// 2. IPv6 VS matcher is reused when the set of IPv6 virtual services remains unchanged
// 3. IPv4/IPv6 VS matcher comparison is order-independent (different VS order = same matcher)
// 4. ACL filters are reused when allowed_srcs configuration remains the same
// 5. ACL comparison is order-independent (different ACL rule order = same ACL)
// 6. ACL comparison handles duplicates correctly
// 7. Partial changes are detected correctly (some VS changed, some unchanged)
//
// This test does NOT send packets - it only analyzes the UpdateInfo structure.
func TestACLAndFilterReuse(t *testing.T) {
	// Create mock Yanet instance
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 512 << 20, // 512 MB
		DpMemory:     64 << 20,  // 64 MB
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{ID: 0, Name: "eth0"},
		},
	})
	require.NoError(t, err)
	defer m.Free()

	// Create logger for tests
	log := zap.NewNop().Sugar()

	// Create balancer agent
	agent, err := NewBalancerAgent(m.SharedMemory(), 256*datasize.MB, log)
	require.NoError(t, err)

	// Helper to create a simple ACL (allow all)
	createSimpleACL := func(isIPv6 bool) []*balancerpb.AllowedSources {
		var addr, mask netip.Addr
		if isIPv6 {
			addr = netip.AddrFrom16([16]byte{})
			mask = netip.AddrFrom16([16]byte{})
		} else {
			addr = netip.AddrFrom4([4]byte{0, 0, 0, 0})
			mask = netip.AddrFrom4([4]byte{0, 0, 0, 0})
		}
		return []*balancerpb.AllowedSources{
			{
				Nets: []*balancerpb.Net{
					{
						Addr: &balancerpb.Addr{Bytes: addr.AsSlice()},
						Mask: &balancerpb.Addr{Bytes: mask.AsSlice()},
					},
				},
			},
		}
	}

	// Helper to create a complex ACL with multiple rules
	createComplexACL := func(variant int, isIPv6 bool) []*balancerpb.AllowedSources {
		var acl []*balancerpb.AllowedSources
		if isIPv6 {
			// Rule 1: 2001:db8:1::/48
			acl = append(acl, &balancerpb.AllowedSources{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{
							0x20, 0x01, 0x0d, 0xb8, 0, 1, 0, 0,
							0, 0, 0, 0, 0, 0, 0, 0,
						}).AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{
							0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0,
							0, 0, 0, 0, 0, 0, 0, 0,
						}).AsSlice(),
					},
				}},
				Ports: []*balancerpb.PortsRange{{From: 1024, To: 65535}},
			})
			// Rule 2: 2001:db8:2::/48 with specific ports
			acl = append(acl, &balancerpb.AllowedSources{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{
							0x20, 0x01, 0x0d, 0xb8, 0, 2, 0, 0,
							0, 0, 0, 0, 0, 0, 0, 0,
						}).AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{
							0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0,
							0, 0, 0, 0, 0, 0, 0, 0,
						}).AsSlice(),
					},
				}},
				Ports: []*balancerpb.PortsRange{
					{From: 80, To: 80},
					{From: 443, To: 443},
				},
			})
		} else {
			// Rule 1: 10.0.0.0/8
			acl = append(acl, &balancerpb.AllowedSources{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{10, 0, 0, 0}).AsSlice()},
					Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 0, 0, 0}).AsSlice()},
				}},
				Ports: []*balancerpb.PortsRange{{From: 1024, To: 65535}},
			})
			// Rule 2: 192.168.0.0/16 with specific ports
			acl = append(acl, &balancerpb.AllowedSources{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{192, 168, 0, 0}).AsSlice()},
					Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice()},
				}},
				Ports: []*balancerpb.PortsRange{{From: 80, To: 80}, {From: 443, To: 443}},
			})
		}

		// Add variant-specific rule
		if variant > 0 {
			if isIPv6 {
				acl = append(acl, &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{
							Bytes: netip.AddrFrom16([16]byte{
								0x20, 0x01, 0x0d, 0xb8, 0, byte(variant), 0, 0,
								0, 0, 0, 0, 0, 0, 0, 0,
							}).AsSlice(),
						},
						Mask: &balancerpb.Addr{
							Bytes: netip.AddrFrom16([16]byte{
								0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0,
								0, 0, 0, 0, 0, 0, 0, 0,
							}).AsSlice(),
						},
					}},
				})
			} else {
				acl = append(acl, &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{172, byte(variant), 0, 0}).AsSlice()},
						Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice()},
					}},
				})
			}
		}
		return acl
	}

	// Helper to create a large complex ACL with 15-20 rules and random duplicates
	// This tests that ACL comparison handles duplicates correctly and works with many rules
	createLargeComplexACL := func(variant int, isIPv6 bool, rng *rand.Rand) []*balancerpb.AllowedSources {
		var acl []*balancerpb.AllowedSources
		numRules := 15 + rng.IntN(6) // 15-20 rules

		for i := 0; i < numRules; i++ {
			var rule *balancerpb.AllowedSources
			if isIPv6 {
				// Generate IPv6 rule with varying prefixes
				addr := [16]byte{
					0x20,
					0x01,
					0x0d,
					0xb8,
					byte(variant),
					byte(i),
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
				}
				mask := [16]byte{
					0xff,
					0xff,
					0xff,
					0xff,
					0xff,
					0xff,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
					0,
				}
				rule = &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{
							Bytes: netip.AddrFrom16(addr).AsSlice(),
						},
						Mask: &balancerpb.Addr{
							Bytes: netip.AddrFrom16(mask).AsSlice(),
						},
					}},
				}
			} else {
				// Generate IPv4 rule with varying prefixes
				addr := [4]byte{byte(10 + variant%240), byte(i), 0, 0}
				mask := [4]byte{255, 255, 0, 0}
				rule = &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4(addr).AsSlice()},
						Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4(mask).AsSlice()},
					}},
				}
			}

			// Add port ranges to some rules
			if i%3 == 0 {
				rule.Ports = []*balancerpb.PortsRange{
					{From: uint32(1024 + i*100), To: uint32(2024 + i*100)},
				}
			} else if i%3 == 1 {
				rule.Ports = []*balancerpb.PortsRange{
					{From: 80, To: 80},
					{From: 443, To: 443},
					{From: uint32(8000 + i), To: uint32(8100 + i)},
				}
			}

			acl = append(acl, rule)

			// Randomly add duplicates (about 30% of rules will be duplicated)
			if rng.Float32() < 0.3 {
				acl = append(acl, rule)
			}
		}

		// Shuffle the ACL to ensure order independence is tested
		rng.Shuffle(len(acl), func(i, j int) {
			acl[i], acl[j] = acl[j], acl[i]
		})

		return acl
	}

	// Helper to create a VS
	createVS := func(ip netip.Addr, port uint16, proto balancerpb.TransportProto, acl []*balancerpb.AllowedSources) *balancerpb.VirtualService {
		var realIP netip.Addr
		var srcAddr, srcMask netip.Addr
		if ip.Is4() {
			realIP = netip.AddrFrom4([4]byte{192, 168, 1, 1})
			srcAddr = netip.AddrFrom4([4]byte{172, 16, 0, 1})
			srcMask = netip.AddrFrom4([4]byte{255, 255, 255, 255})
		} else {
			realIP = netip.AddrFrom16([16]byte{0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
			srcAddr = netip.AddrFrom16([16]byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
			srcMask = netip.AddrFrom16([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
		}

		return &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
				Port:  uint32(port),
				Proto: proto,
			},
			Scheduler:   balancerpb.VsScheduler_ROUND_ROBIN,
			AllowedSrcs: acl,
			Reals: []*balancerpb.Real{
				{
					Id: &balancerpb.RelativeRealIdentifier{
						Ip:   &balancerpb.Addr{Bytes: realIP.AsSlice()},
						Port: 0,
					},
					Weight:  100,
					SrcAddr: &balancerpb.Addr{Bytes: srcAddr.AsSlice()},
					SrcMask: &balancerpb.Addr{Bytes: srcMask.AsSlice()},
				},
			},
			Flags: &balancerpb.VsFlags{},
			Peers: []*balancerpb.Addr{},
		}
	}

	// Helper to create config
	createConfig := func(vsList []*balancerpb.VirtualService) *balancerpb.BalancerConfig {
		return &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
				},
				Vs:             vsList,
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

	// Helper to generate many virtual services for large-scale tests
	// ipBase parameter allows using different IP ranges to avoid overlap between tests
	generateManyVS := func(numIPv4, numIPv6 int, ipBase byte, aclGenerator func(idx int, isIPv6 bool) []*balancerpb.AllowedSources) []*balancerpb.VirtualService {
		vsList := make([]*balancerpb.VirtualService, 0, numIPv4+numIPv6)

		// Generate IPv4 VS
		for i := 0; i < numIPv4; i++ {
			ip := netip.AddrFrom4(
				[4]byte{ipBase, byte(i / 256), byte(i % 256), 1},
			)
			proto := balancerpb.TransportProto_TCP
			if i%3 == 0 {
				proto = balancerpb.TransportProto_UDP
			}
			port := uint16(80 + (i % 10))
			acl := aclGenerator(i, false)
			vsList = append(vsList, createVS(ip, port, proto, acl))
		}

		// Generate IPv6 VS
		for i := 0; i < numIPv6; i++ {
			ip := netip.AddrFrom16([16]byte{
				0x20, 0x01, 0x0d, 0xb8,
				ipBase, byte(i), 0, 0,
				0, 0, 0, 0, 0, 0, 0, 1,
			})
			proto := balancerpb.TransportProto_TCP
			if i%3 == 0 {
				proto = balancerpb.TransportProto_UDP
			}
			port := uint16(80 + (i % 10))
			acl := aclGenerator(i, true)
			vsList = append(vsList, createVS(ip, port, proto, acl))
		}

		return vsList
	}

	// Helper to verify UpdateInfo
	verifyUpdateInfo := func(t *testing.T, info *ffi.UpdateInfo, expectIPv4Reused, expectIPv6Reused bool, expectACLReusedCount int) {
		t.Helper()
		assert.Equal(
			t,
			expectIPv4Reused,
			info.VsIpv4MatcherReused,
			"IPv4 matcher reuse mismatch",
		)
		assert.Equal(
			t,
			expectIPv6Reused,
			info.VsIpv6MatcherReused,
			"IPv6 matcher reuse mismatch",
		)
		assert.Equal(
			t,
			expectACLReusedCount,
			len(info.ACLReusedVs),
			"ACL reused count mismatch",
		)
	}

	// Test 1: Initial configuration
	t.Run("InitialConfiguration", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			),
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			),
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		err := agent.NewBalancerManager("test", config)
		require.NoError(t, err)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)
		require.NotNil(t, manager)
	})

	// Test 2: Identical configuration - everything should be reused
	t.Run("IdenticalConfiguration", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			),
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			),
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Everything should be reused
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 3: VS order independence - IPv4 VS in different order
	t.Run("IPv4_VSOrderIndependence", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			// Swap IPv4 VS order
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			),
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			),
			// Keep IPv6 VS order the same
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers should be reused (order doesn't matter), all ACLs reused
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 4: VS order independence - IPv6 VS in different order
	t.Run("IPv6_VSOrderIndependence", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			// Keep IPv4 VS order from previous test
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			),
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			),
			// Swap IPv6 VS order
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers should be reused (order doesn't matter), all ACLs reused
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 5: VS order independence - both IPv4 and IPv6 in different order
	t.Run("BothIPv4AndIPv6_VSOrderIndependence", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			// Different IPv4 order
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			),
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			),
			// Different IPv6 order
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers should be reused (order doesn't matter), all ACLs reused
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 6: Same VS identifiers, different ACL for some VS
	t.Run("SameVS_DifferentACLForSome", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(2, false),
			), // Changed ACL
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // Same ACL
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			), // Same ACL
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(2, true),
			), // Changed ACL
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers reused (VS identifiers unchanged), but only 2 ACLs reused
		verifyUpdateInfo(t, updateInfo, true, true, 2)
	})

	// Test 7: ACL order independence - shuffled ACL should be considered the same
	t.Run("ACLOrderIndependence", func(t *testing.T) {
		// Create ACL with rules in different order
		acl1 := createComplexACL(2, false)
		acl2 := make([]*balancerpb.AllowedSources, len(acl1))
		// Reverse order
		for i := range acl1 {
			acl2[len(acl1)-1-i] = acl1[i]
		}

		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl2,
			), // Reversed order, should match
			createVS(
				netip.MustParseAddr("10.0.0.2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // Same as before
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(2, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (order doesn't matter)
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 8: Different IPv4 VS set, same IPv6 VS set
	t.Run("DifferentIPv4_SameIPv6", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.3"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			), // New IPv4 VS
			createVS(
				netip.MustParseAddr("10.0.0.4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // New IPv4 VS
			createVS(
				netip.MustParseAddr("2001:db8::1"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			), // Same IPv6 VS
			createVS(
				netip.MustParseAddr("2001:db8::2"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(2, true),
			), // Same IPv6 VS
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// IPv4 matcher NOT reused (different VS set), IPv6 matcher reused, 2 IPv6 ACLs reused
		verifyUpdateInfo(t, updateInfo, false, true, 2)
	})

	// Test 9: Same IPv4 VS set, different IPv6 VS set
	t.Run("SameIPv4_DifferentIPv6", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.3"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(false),
			), // Same IPv4 VS
			createVS(
				netip.MustParseAddr("10.0.0.4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // Same IPv4 VS
			createVS(
				netip.MustParseAddr("2001:db8::3"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			), // New IPv6 VS
			createVS(
				netip.MustParseAddr("2001:db8::4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			), // New IPv6 VS
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// IPv4 matcher reused, IPv6 matcher NOT reused (different VS set), 2 IPv4 ACLs reused
		verifyUpdateInfo(t, updateInfo, true, false, 2)
	})

	// Test 10: Protocol change for some VS
	t.Run("ProtocolChange", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.3"),
				80,
				balancerpb.TransportProto_UDP,
				createSimpleACL(false),
			), // Changed protocol
			createVS(
				netip.MustParseAddr("10.0.0.4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // Same
			createVS(
				netip.MustParseAddr("2001:db8::3"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// IPv4 matcher NOT reused (protocol changed = different VS identifier), IPv6 reused
		verifyUpdateInfo(
			t,
			updateInfo,
			false,
			true,
			3,
		) // 1 IPv4 VS with same ACL + 2 IPv6 VS
	})

	// Test 11: Port change for some VS
	t.Run("PortChange", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.0.3"),
				8080,
				balancerpb.TransportProto_UDP,
				createSimpleACL(false),
			), // Changed port
			createVS(
				netip.MustParseAddr("10.0.0.4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, false),
			), // Same
			createVS(
				netip.MustParseAddr("2001:db8::3"),
				80,
				balancerpb.TransportProto_TCP,
				createSimpleACL(true),
			),
			createVS(
				netip.MustParseAddr("2001:db8::4"),
				80,
				balancerpb.TransportProto_TCP,
				createComplexACL(1, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// IPv4 matcher NOT reused (port changed = different VS identifier), IPv6 reused
		verifyUpdateInfo(t, updateInfo, false, true, 3) // 1 IPv4 VS + 2 IPv6 VS
	})

	// Test 12: Completely different configuration
	t.Run("CompletelyDifferent", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.1.1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, false),
			),
			createVS(
				netip.MustParseAddr("10.0.1.2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, false),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, true),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused
		verifyUpdateInfo(t, updateInfo, false, false, 0)
	})

	// Test 13: Back to previous configuration - everything should be reused
	t.Run("BackToPrevious", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.1.1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, false),
			),
			createVS(
				netip.MustParseAddr("10.0.1.2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, false),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, true),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Everything reused (same as previous)
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 14: ACL with duplicates - should be considered the same
	t.Run("ACLWithDuplicates", func(t *testing.T) {
		acl := createComplexACL(3, false)
		aclWithDuplicates := make([]*balancerpb.AllowedSources, 0, len(acl)*2)
		for _, rule := range acl {
			aclWithDuplicates = append(aclWithDuplicates, rule)
			aclWithDuplicates = append(
				aclWithDuplicates,
				rule,
			) // Duplicate each rule
		}

		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.1.1"),
				443,
				balancerpb.TransportProto_TCP,
				aclWithDuplicates,
			), // ACL with duplicates
			createVS(
				netip.MustParseAddr("10.0.1.2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, false),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, true),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (duplicates don't matter)
		verifyUpdateInfo(t, updateInfo, true, true, 4)
	})

	// Test 15: Mixed IPv4/IPv6 VS order with ACL changes
	t.Run("MixedOrderWithACLChanges", func(t *testing.T) {
		vsList := []*balancerpb.VirtualService{
			// Different order and one ACL change
			createVS(
				netip.MustParseAddr("10.0.1.2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(5, false),
			), // Changed ACL
			createVS(
				netip.MustParseAddr("10.0.1.1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, false),
			), // Same ACL (with duplicates from prev test)
			createVS(
				netip.MustParseAddr("2001:db8:1::2"),
				443,
				balancerpb.TransportProto_UDP,
				createComplexACL(4, true),
			),
			createVS(
				netip.MustParseAddr("2001:db8:1::1"),
				443,
				balancerpb.TransportProto_TCP,
				createComplexACL(3, true),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers reused (same VS set, different order), 3 ACLs reused (1 IPv4 changed)
		verifyUpdateInfo(t, updateInfo, true, true, 3)
	})

	// Test 16: Large complex ACL with 15-20 rules and random duplicates
	t.Run("LargeComplexACLWithDuplicates", func(t *testing.T) {
		rng := rand.New(
			rand.NewPCG(42, 0),
		) // Deterministic seed for reproducibility

		// Create initial configuration with large complex ACLs
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.2.1"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(1, false, rng),
			),
			createVS(
				netip.MustParseAddr("10.0.2.2"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(2, false, rng),
			),
			createVS(
				netip.MustParseAddr("2001:db8:2::1"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(1, true, rng),
			),
			createVS(
				netip.MustParseAddr("2001:db8:2::2"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(2, true, rng),
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused (completely new VS set)
		verifyUpdateInfo(t, updateInfo, false, false, 0)

		// Now update with the same ACLs but regenerated with same seed (should be identical)
		rng2 := rand.New(rand.NewPCG(42, 0)) // Same seed
		vsList2 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.2.1"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(1, false, rng2),
			),
			createVS(
				netip.MustParseAddr("10.0.2.2"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(2, false, rng2),
			),
			createVS(
				netip.MustParseAddr("2001:db8:2::1"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(1, true, rng2),
			),
			createVS(
				netip.MustParseAddr("2001:db8:2::2"),
				80,
				balancerpb.TransportProto_TCP,
				createLargeComplexACL(2, true, rng2),
			),
		}
		config2 := createConfig(vsList2)

		updateInfo2, err := manager.Update(config2, m.CurrentTime())
		require.NoError(t, err)

		// Everything should be reused (same ACLs despite duplicates and shuffling)
		verifyUpdateInfo(t, updateInfo2, true, true, 4)
	})

	// Test 17: Many virtual services (15 IPv4 + 15 IPv6 = 30 total)
	t.Run("ManyVirtualServices", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(100, 0))

		// Generate 15 IPv4 and 15 IPv6 VS with large complex ACLs
		// Use ipBase=10 for this test group
		vsList := generateManyVS(
			15,
			15,
			10,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				return createLargeComplexACL(idx, isIPv6, rng)
			},
		)
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused (completely new VS set)
		verifyUpdateInfo(t, updateInfo, false, false, 0)

		// Update with identical configuration
		rng2 := rand.New(rand.NewPCG(100, 0)) // Same seed
		vsList2 := generateManyVS(
			15,
			15,
			10,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				return createLargeComplexACL(idx, isIPv6, rng2)
			},
		)
		config2 := createConfig(vsList2)

		updateInfo2, err := manager.Update(config2, m.CurrentTime())
		require.NoError(t, err)

		// Everything should be reused (30 VS total)
		verifyUpdateInfo(t, updateInfo2, true, true, 30)
	})

	// Test 18: Many VS with shuffled order - should still reuse
	t.Run("ManyVS_ShuffledOrder", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(100, 0))

		// Generate same VS as previous test (ipBase=10)
		vsList := generateManyVS(
			15,
			15,
			10,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				return createLargeComplexACL(idx, isIPv6, rng)
			},
		)

		// Shuffle the VS list
		shuffleRng := rand.New(rand.NewPCG(999, 0))
		shuffleRng.Shuffle(len(vsList), func(i, j int) {
			vsList[i], vsList[j] = vsList[j], vsList[i]
		})

		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers should be reused (order doesn't matter), all 30 ACLs reused
		verifyUpdateInfo(t, updateInfo, true, true, 30)
	})

	// Test 19: Many VS with some ACL changes
	t.Run("ManyVS_SomeACLChanges", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(100, 0))

		// Generate VS with same base but change ACL for first 5 IPv4 and first 5 IPv6 (ipBase=10)
		vsList := generateManyVS(
			15,
			15,
			10,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				// Change ACL for indices 0-4 by using different variant
				if idx < 5 {
					return createLargeComplexACL(
						idx+100,
						isIPv6,
						rng,
					) // Different variant
				}
				return createLargeComplexACL(idx, isIPv6, rng)
			},
		)
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Both matchers reused (same VS identifiers), but only 20 ACLs reused (10 changed)
		verifyUpdateInfo(t, updateInfo, true, true, 20)
	})

	// Test 20: Large scale with 25 IPv4 + 25 IPv6 = 50 VS total
	// Use different IP range (ipBase=20) to avoid overlap with previous tests
	t.Run("LargeScale_50VS", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(200, 0))

		// Generate 25 IPv4 and 25 IPv6 VS with ipBase=20 (different from previous tests)
		vsList := generateManyVS(
			25,
			25,
			20,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				return createLargeComplexACL(idx, isIPv6, rng)
			},
		)
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused (completely new VS set with different IP range)
		verifyUpdateInfo(t, updateInfo, false, false, 0)

		// Update with identical configuration
		rng2 := rand.New(rand.NewPCG(200, 0)) // Same seed
		vsList2 := generateManyVS(
			25,
			25,
			20,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				return createLargeComplexACL(idx, isIPv6, rng2)
			},
		)
		config2 := createConfig(vsList2)

		updateInfo2, err := manager.Update(config2, m.CurrentTime())
		require.NoError(t, err)

		// Everything should be reused (50 VS total)
		verifyUpdateInfo(t, updateInfo2, true, true, 50)
	})

	// Test 21: Large scale with shuffled ACL rules (order independence with many rules)
	t.Run("LargeScale_ShuffledACLRules", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(200, 0))

		// Generate VS with same ACLs but shuffle the rules within each ACL (ipBase=20)
		vsList := generateManyVS(
			25,
			25,
			20,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				acl := createLargeComplexACL(idx, isIPv6, rng)
				// Additional shuffle of the ACL rules
				shuffleRng := rand.New(rand.NewPCG(uint64(idx+1000), 0))
				shuffleRng.Shuffle(len(acl), func(i, j int) {
					acl[i], acl[j] = acl[j], acl[i]
				})
				return acl
			},
		)
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (order doesn't matter even with many rules)
		verifyUpdateInfo(t, updateInfo, true, true, 50)
	})

	// Test 22: Large scale with additional duplicates in ACLs
	t.Run("LargeScale_ExtraDuplicates", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(200, 0))

		// Generate VS with same ACLs but add extra duplicates (ipBase=20)
		vsList := generateManyVS(
			25,
			25,
			20,
			func(idx int, isIPv6 bool) []*balancerpb.AllowedSources {
				acl := createLargeComplexACL(idx, isIPv6, rng)
				// Add extra duplicates (duplicate first 5 rules again)
				extraDuplicates := make(
					[]*balancerpb.AllowedSources,
					0,
					len(acl)+5,
				)
				extraDuplicates = append(extraDuplicates, acl...)
				for i := 0; i < 5 && i < len(acl); i++ {
					extraDuplicates = append(extraDuplicates, acl[i])
				}
				// Shuffle
				shuffleRng := rand.New(rand.NewPCG(uint64(idx+2000), 0))
				shuffleRng.Shuffle(len(extraDuplicates), func(i, j int) {
					extraDuplicates[i], extraDuplicates[j] = extraDuplicates[j], extraDuplicates[i]
				})
				return extraDuplicates
			},
		)
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (duplicates don't matter)
		verifyUpdateInfo(t, updateInfo, true, true, 50)
	})

	// Test 23: ACL with different tags - should be considered equal (tags don't affect ACL comparison)
	t.Run("ACLWithDifferentTags_ShouldBeEqual", func(t *testing.T) {
		// Create initial config with specific tags
		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.3.1"),
				80,
				balancerpb.TransportProto_TCP,
				[]*balancerpb.AllowedSources{
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.0.0.0").
									AsSlice(),
							},
						}},
						Ports: []*balancerpb.PortsRange{
							{From: 1024, To: 65535},
						},
						Tag: 100, // Tag = 100
					},
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.168.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.0.0").
									AsSlice(),
							},
						}},
						Ports: []*balancerpb.PortsRange{
							{From: 80, To: 80},
							{From: 443, To: 443},
						},
						Tag: 200, // Tag = 200
					},
				},
			),
			createVS(
				netip.MustParseAddr("10.0.3.2"),
				80,
				balancerpb.TransportProto_TCP,
				[]*balancerpb.AllowedSources{
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.0.0").
									AsSlice(),
							},
						}},
						Tag: 300, // Tag = 300
					},
				},
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused (new VS set)
		verifyUpdateInfo(t, updateInfo, false, false, 0)

		// Update with same ACL rules but different tags - should be considered equal
		vsList2 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.3.1"),
				80,
				balancerpb.TransportProto_TCP,
				[]*balancerpb.AllowedSources{
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.0.0.0").
									AsSlice(),
							},
						}},
						Ports: []*balancerpb.PortsRange{
							{From: 1024, To: 65535},
						},
						Tag: 999, // Different tag (was 100)
					},
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.168.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.0.0").
									AsSlice(),
							},
						}},
						Ports: []*balancerpb.PortsRange{
							{From: 80, To: 80},
							{From: 443, To: 443},
						},
						Tag: 888, // Different tag (was 200)
					},
				},
			),
			createVS(
				netip.MustParseAddr("10.0.3.2"),
				80,
				balancerpb.TransportProto_TCP,
				[]*balancerpb.AllowedSources{
					{
						Nets: []*balancerpb.Net{{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.0.0").
									AsSlice(),
							},
							Mask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.0.0").
									AsSlice(),
							},
						}},
						Tag: 0, // Different tag (was 300)
					},
				},
			),
		}
		config2 := createConfig(vsList2)

		updateInfo2, err := manager.Update(config2, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (tags don't affect ACL comparison)
		verifyUpdateInfo(t, updateInfo2, true, true, 2)
	})

	// Test 24: Verify tag values are preserved in config after update
	t.Run("TagValuesPreservedInConfig", func(t *testing.T) {
		// Get current config and verify tag values
		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		config := manager.Config()
		require.NotNil(t, config)
		require.NotNil(t, config.PacketHandler)
		require.Len(t, config.PacketHandler.Vs, 2)

		// Verify first VS tags
		require.Len(t, config.PacketHandler.Vs[0].AllowedSrcs, 2)
		assert.Equal(
			t,
			uint32(999),
			config.PacketHandler.Vs[0].AllowedSrcs[0].Tag,
			"first VS first tag should be 999",
		)
		assert.Equal(
			t,
			uint32(888),
			config.PacketHandler.Vs[0].AllowedSrcs[1].Tag,
			"first VS second tag should be 888",
		)

		// Verify second VS tag
		require.Len(t, config.PacketHandler.Vs[1].AllowedSrcs, 1)
		assert.Equal(
			t,
			uint32(0),
			config.PacketHandler.Vs[1].AllowedSrcs[0].Tag,
			"second VS tag should be 0",
		)
	})

	// Test 25: ACL reuse with many nets and port ranges in different order and with different tags
	t.Run("ACLReuseWithManyNetsAndPortRanges", func(t *testing.T) {
		// Helper to create AllowedSources with many nets and port ranges
		createManyNetsACL := func(variant int, isIPv6 bool, tag uint32, rng *rand.Rand) []*balancerpb.AllowedSources {
			numNets := 10 + rng.IntN(6) // 10-15 nets
			numPorts := 3 + rng.IntN(3) // 3-5 port ranges

			nets := make([]*balancerpb.Net, numNets)
			for i := range numNets {
				if isIPv6 {
					// Generate IPv6 networks
					addr := [16]byte{
						0x20, 0x01, 0x0d, 0xb8,
						byte(variant), byte(i), 0, 0,
						0, 0, 0, 0, 0, 0, 0, 0,
					}
					mask := [16]byte{
						0xff, 0xff, 0xff, 0xff,
						0xff, 0xff, 0, 0,
						0, 0, 0, 0, 0, 0, 0, 0,
					}
					nets[i] = &balancerpb.Net{
						Addr: &balancerpb.Addr{
							Bytes: netip.AddrFrom16(addr).AsSlice(),
						},
						Mask: &balancerpb.Addr{
							Bytes: netip.AddrFrom16(mask).AsSlice(),
						},
					}
				} else {
					// Generate IPv4 networks
					addr := [4]byte{byte(10 + variant%240), byte(i), 0, 0}
					mask := [4]byte{255, 255, 0, 0}
					nets[i] = &balancerpb.Net{
						Addr: &balancerpb.Addr{Bytes: netip.AddrFrom4(addr).AsSlice()},
						Mask: &balancerpb.Addr{Bytes: netip.AddrFrom4(mask).AsSlice()},
					}
				}
			}

			ports := make([]*balancerpb.PortsRange, numPorts)
			for i := range numPorts {
				switch i {
				case 0:
					ports[i] = &balancerpb.PortsRange{From: 80, To: 80}
				case 1:
					ports[i] = &balancerpb.PortsRange{From: 443, To: 443}
				case 2:
					ports[i] = &balancerpb.PortsRange{From: 1024, To: 2048}
				case 3:
					ports[i] = &balancerpb.PortsRange{From: 8000, To: 9000}
				case 4:
					ports[i] = &balancerpb.PortsRange{From: 3000, To: 3999}
				}
			}

			return []*balancerpb.AllowedSources{
				{
					Nets:  nets,
					Ports: ports,
					Tag:   tag,
				},
			}
		}

		// Helper to shuffle nets in AllowedSources
		shuffleNets := func(acl []*balancerpb.AllowedSources, rng *rand.Rand) []*balancerpb.AllowedSources {
			result := make([]*balancerpb.AllowedSources, len(acl))
			for i, rule := range acl {
				newNets := make([]*balancerpb.Net, len(rule.Nets))
				copy(newNets, rule.Nets)
				rng.Shuffle(len(newNets), func(i, j int) {
					newNets[i], newNets[j] = newNets[j], newNets[i]
				})
				result[i] = &balancerpb.AllowedSources{
					Nets:  newNets,
					Ports: rule.Ports,
					Tag:   rule.Tag,
				}
			}
			return result
		}

		// Helper to shuffle port ranges in AllowedSources
		shufflePorts := func(acl []*balancerpb.AllowedSources, rng *rand.Rand) []*balancerpb.AllowedSources {
			result := make([]*balancerpb.AllowedSources, len(acl))
			for i, rule := range acl {
				newPorts := make([]*balancerpb.PortsRange, len(rule.Ports))
				copy(newPorts, rule.Ports)
				rng.Shuffle(len(newPorts), func(i, j int) {
					newPorts[i], newPorts[j] = newPorts[j], newPorts[i]
				})
				result[i] = &balancerpb.AllowedSources{
					Nets:  rule.Nets,
					Ports: newPorts,
					Tag:   rule.Tag,
				}
			}
			return result
		}

		// Helper to change tags in AllowedSources
		changeTags := func(acl []*balancerpb.AllowedSources, newTag uint32) []*balancerpb.AllowedSources {
			result := make([]*balancerpb.AllowedSources, len(acl))
			for i, rule := range acl {
				result[i] = &balancerpb.AllowedSources{
					Nets:  rule.Nets,
					Ports: rule.Ports,
					Tag:   newTag,
				}
			}
			return result
		}

		rng := rand.New(rand.NewPCG(300, 0)) // Deterministic seed

		// Scenario 1: Initial configuration with many nets and port ranges
		acl1IPv4 := createManyNetsACL(1, false, 100, rng)
		acl2IPv4 := createManyNetsACL(2, false, 200, rng)
		acl1IPv6 := createManyNetsACL(1, true, 100, rng)
		acl2IPv6 := createManyNetsACL(2, true, 200, rng)

		vsList := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6,
			),
		}
		config := createConfig(vsList)

		manager, err := agent.BalancerManager("test")
		require.NoError(t, err)

		updateInfo, err := manager.Update(config, m.CurrentTime())
		require.NoError(t, err)

		// Nothing reused (new VS set)
		verifyUpdateInfo(t, updateInfo, false, false, 0)

		// Scenario 2: Same ACL with shuffled net order
		shuffleRng := rand.New(rand.NewPCG(301, 0))
		acl1IPv4Shuffled := shuffleNets(acl1IPv4, shuffleRng)
		acl2IPv4Shuffled := shuffleNets(acl2IPv4, shuffleRng)
		acl1IPv6Shuffled := shuffleNets(acl1IPv6, shuffleRng)
		acl2IPv6Shuffled := shuffleNets(acl2IPv6, shuffleRng)

		vsList2 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4Shuffled,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Shuffled,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Shuffled,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Shuffled,
			),
		}
		config2 := createConfig(vsList2)

		updateInfo2, err := manager.Update(config2, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (net order doesn't matter)
		verifyUpdateInfo(t, updateInfo2, true, true, 4)

		// Scenario 3: Same ACL with shuffled port range order
		shuffleRng2 := rand.New(rand.NewPCG(302, 0))
		acl1IPv4PortShuffled := shufflePorts(acl1IPv4, shuffleRng2)
		acl2IPv4PortShuffled := shufflePorts(acl2IPv4, shuffleRng2)
		acl1IPv6PortShuffled := shufflePorts(acl1IPv6, shuffleRng2)
		acl2IPv6PortShuffled := shufflePorts(acl2IPv6, shuffleRng2)

		vsList3 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4PortShuffled,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4PortShuffled,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6PortShuffled,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6PortShuffled,
			),
		}
		config3 := createConfig(vsList3)

		updateInfo3, err := manager.Update(config3, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (port range order doesn't matter)
		verifyUpdateInfo(t, updateInfo3, true, true, 4)

		// Scenario 4: Same ACL with different tags
		acl1IPv4NewTag := changeTags(acl1IPv4, 999)
		acl2IPv4NewTag := changeTags(acl2IPv4, 888)
		acl1IPv6NewTag := changeTags(acl1IPv6, 777)
		acl2IPv6NewTag := changeTags(acl2IPv6, 666)

		vsList4 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4NewTag,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4NewTag,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6NewTag,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6NewTag,
			),
		}
		config4 := createConfig(vsList4)

		updateInfo4, err := manager.Update(config4, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (tags don't affect ACL comparison)
		verifyUpdateInfo(t, updateInfo4, true, true, 4)

		// Scenario 5: Combined - shuffled nets, shuffled ports, and different tags
		shuffleRng3 := rand.New(rand.NewPCG(303, 0))
		shuffleRng4 := rand.New(rand.NewPCG(304, 0))

		acl1IPv4Combined := shuffleNets(acl1IPv4, shuffleRng3)
		acl1IPv4Combined = shufflePorts(acl1IPv4Combined, shuffleRng4)
		acl1IPv4Combined = changeTags(acl1IPv4Combined, 111)

		acl2IPv4Combined := shuffleNets(acl2IPv4, shuffleRng3)
		acl2IPv4Combined = shufflePorts(acl2IPv4Combined, shuffleRng4)
		acl2IPv4Combined = changeTags(acl2IPv4Combined, 222)

		acl1IPv6Combined := shuffleNets(acl1IPv6, shuffleRng3)
		acl1IPv6Combined = shufflePorts(acl1IPv6Combined, shuffleRng4)
		acl1IPv6Combined = changeTags(acl1IPv6Combined, 333)

		acl2IPv6Combined := shuffleNets(acl2IPv6, shuffleRng3)
		acl2IPv6Combined = shufflePorts(acl2IPv6Combined, shuffleRng4)
		acl2IPv6Combined = changeTags(acl2IPv6Combined, 444)

		vsList5 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config5 := createConfig(vsList5)

		updateInfo5, err := manager.Update(config5, m.CurrentTime())
		require.NoError(t, err)

		// All ACLs should be reused (order and tags don't matter)
		verifyUpdateInfo(t, updateInfo5, true, true, 4)

		// Verify that tags are preserved in the final config
		finalConfig := manager.Config()
		require.NotNil(t, finalConfig)
		require.NotNil(t, finalConfig.PacketHandler)
		require.Len(t, finalConfig.PacketHandler.Vs, 4)

		// Check that the new tags are stored correctly
		assert.Equal(
			t,
			uint32(111),
			finalConfig.PacketHandler.Vs[0].AllowedSrcs[0].Tag,
			"first IPv4 VS tag should be 111",
		)
		assert.Equal(
			t,
			uint32(222),
			finalConfig.PacketHandler.Vs[1].AllowedSrcs[0].Tag,
			"second IPv4 VS tag should be 222",
		)
		assert.Equal(
			t,
			uint32(333),
			finalConfig.PacketHandler.Vs[2].AllowedSrcs[0].Tag,
			"first IPv6 VS tag should be 333",
		)
		assert.Equal(
			t,
			uint32(444),
			finalConfig.PacketHandler.Vs[3].AllowedSrcs[0].Tag,
			"second IPv6 VS tag should be 444",
		)

		// Scenario 6: ALMOST matching - one net is different (should NOT reuse)
		rng6 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4AlmostMatch := createManyNetsACL(1, false, 111, rng6)
		// Modify one net in the middle
		acl1IPv4AlmostMatch[0].Nets[5] = &balancerpb.Net{
			Addr: &balancerpb.Addr{
				Bytes: netip.AddrFrom4([4]byte{99, 99, 0, 0}).AsSlice(),
			},
			Mask: &balancerpb.Addr{
				Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice(),
			},
		}

		vsList6 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4AlmostMatch,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config6 := createConfig(vsList6)

		updateInfo6, err := manager.Update(config6, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has different net)
		verifyUpdateInfo(t, updateInfo6, true, true, 3)

		// Scenario 7: ALMOST matching - one port range is different (should NOT reuse)
		rng7 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4AlmostMatchPort := createManyNetsACL(1, false, 111, rng7)
		// Modify one port range
		acl1IPv4AlmostMatchPort[0].Ports[1] = &balancerpb.PortsRange{
			From: 8443,
			To:   8443,
		} // Changed from 443

		vsList7 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4AlmostMatchPort,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config7 := createConfig(vsList7)

		updateInfo7, err := manager.Update(config7, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has different port range)
		verifyUpdateInfo(t, updateInfo7, true, true, 3)

		// Scenario 8: ALMOST matching - one net is missing (should NOT reuse)
		rng8 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4MissingNet := createManyNetsACL(1, false, 111, rng8)
		// Remove one net from the middle
		acl1IPv4MissingNet[0].Nets = append(
			acl1IPv4MissingNet[0].Nets[:3],
			acl1IPv4MissingNet[0].Nets[4:]...)

		vsList8 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4MissingNet,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config8 := createConfig(vsList8)

		updateInfo8, err := manager.Update(config8, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has missing net)
		verifyUpdateInfo(t, updateInfo8, true, true, 3)

		// Scenario 9: ALMOST matching - one port range is missing (should NOT reuse)
		rng9 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4MissingPort := createManyNetsACL(1, false, 111, rng9)
		// Remove one port range
		acl1IPv4MissingPort[0].Ports = acl1IPv4MissingPort[0].Ports[:len(acl1IPv4MissingPort[0].Ports)-1]

		vsList9 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4MissingPort,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config9 := createConfig(vsList9)

		updateInfo9, err := manager.Update(config9, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has missing port range)
		verifyUpdateInfo(t, updateInfo9, true, true, 3)

		// Scenario 10: ALMOST matching - one extra net added (should NOT reuse)
		rng10 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4ExtraNet := createManyNetsACL(1, false, 111, rng10)
		// Add one extra net
		acl1IPv4ExtraNet[0].Nets = append(
			acl1IPv4ExtraNet[0].Nets,
			&balancerpb.Net{
				Addr: &balancerpb.Addr{
					Bytes: netip.AddrFrom4([4]byte{88, 88, 0, 0}).AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.AddrFrom4([4]byte{255, 255, 0, 0}).AsSlice(),
				},
			},
		)

		vsList10 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4ExtraNet,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config10 := createConfig(vsList10)

		updateInfo10, err := manager.Update(config10, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has extra net)
		verifyUpdateInfo(t, updateInfo10, true, true, 3)

		// Scenario 11: ALMOST matching - one extra port range added (should NOT reuse)
		rng11 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4ExtraPort := createManyNetsACL(1, false, 111, rng11)
		// Add one extra port range
		acl1IPv4ExtraPort[0].Ports = append(
			acl1IPv4ExtraPort[0].Ports,
			&balancerpb.PortsRange{From: 9999, To: 9999},
		)

		vsList11 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4ExtraPort,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config11 := createConfig(vsList11)

		updateInfo11, err := manager.Update(config11, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has extra port range)
		verifyUpdateInfo(t, updateInfo11, true, true, 3)

		// Scenario 12: ALMOST matching - net mask is different (should NOT reuse)
		rng12 := rand.New(rand.NewPCG(300, 0))
		acl1IPv4DifferentMask := createManyNetsACL(1, false, 111, rng12)
		// Change mask of one net
		acl1IPv4DifferentMask[0].Nets[2].Mask = &balancerpb.Addr{
			Bytes: netip.AddrFrom4([4]byte{255, 255, 255, 0}).AsSlice(),
		} // Changed from /16 to /24

		vsList12 := []*balancerpb.VirtualService{
			createVS(
				netip.MustParseAddr("10.0.4.1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv4DifferentMask,
			),
			createVS(
				netip.MustParseAddr("10.0.4.2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv4Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::1"),
				80,
				balancerpb.TransportProto_TCP,
				acl1IPv6Combined,
			),
			createVS(
				netip.MustParseAddr("2001:db8:4::2"),
				80,
				balancerpb.TransportProto_TCP,
				acl2IPv6Combined,
			),
		}
		config12 := createConfig(vsList12)

		updateInfo12, err := manager.Update(config12, m.CurrentTime())
		require.NoError(t, err)

		// Only 3 ACLs should be reused (first IPv4 VS has different mask)
		verifyUpdateInfo(t, updateInfo12, true, true, 3)
	})
}
