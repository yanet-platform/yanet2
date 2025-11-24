package lib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCIDRExpansion_IPv4(t *testing.T) {
	// Test that IP(dst="172.20.29.5/30") generates packets for all IPs in subnet
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "test-send.pcap",
				"expect_file": "test-expect.pcap",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {
									"dst": "00:11:22:33:44:55",
									"src": "00:00:00:00:00:01"
								}
							},
							{
								"type": "IP",
								"params": {
									"src": "192.168.1.1",
									"dst": "172.20.29.5/30",
									"ttl": 64,
									"_special": {
										"dst": {
											"type": "cidr_expansion",
											"cidr": "172.20.29.5/30"
										}
									}
								}
							},
							{
								"type": "TCP",
								"params": {
									"sport": 1234,
									"dport": 80
								}
							}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotEmpty(t, code)

	// Check that code contains CIDR expansion loop
	require.Contains(t, code, `lib.ExpandCIDR("172.20.29.5/30")`)
	require.Contains(t, code, "for _, ip := range")

	// Check that it uses the loop variable
	require.Contains(t, code, "lib.IPDst(ip)")

	// Should NOT contain the original CIDR in the IP option
	require.NotContains(t, code, `lib.IPDst("172.20.29.5/30")`)

	// Should contain the base IP for other fields
	require.Contains(t, code, `lib.IPSrc("192.168.1.1")`)

	t.Logf("Generated code:\n%s", code)
}

func TestCIDRExpansion_IPv6(t *testing.T) {
	// Test that IPv6(src="2000::/126") generates packets for all IPs in subnet
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "test-send.pcap",
				"expect_file": "test-expect.pcap",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {
									"dst": "00:11:22:33:44:55",
									"src": "00:00:00:00:00:01"
								}
							},
							{
								"type": "IPv6",
								"params": {
									"src": "2000::/126",
									"dst": "2222::1",
									"hlim": 64,
									"_special": {
										"src": {
											"type": "cidr_expansion",
											"cidr": "2000::/126"
										}
									}
								}
							},
							{
								"type": "TCP",
								"params": {
									"sport": 2000,
									"dport": 80
								}
							}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotEmpty(t, code)

	// Check that code contains CIDR expansion loop
	require.Contains(t, code, `lib.ExpandCIDR("2000::/126")`)
	require.Contains(t, code, "for _, ip := range")

	// Check that it uses the loop variable
	require.Contains(t, code, "lib.IPv6Src(ip)")

	// Should NOT contain the original CIDR in the IPv6 option
	require.NotContains(t, code, `lib.IPv6Src("2000::/126")`)

	t.Logf("Generated code:\n%s", code)
}

func TestExpandCIDR_IPv4_Small(t *testing.T) {
	// Test /30 subnet (4 addresses)
	ips := ExpandCIDR("172.20.29.5/30")
	require.Len(t, ips, 4)
	require.Equal(t, "172.20.29.4", ips[0])
	require.Equal(t, "172.20.29.5", ips[1])
	require.Equal(t, "172.20.29.6", ips[2])
	require.Equal(t, "172.20.29.7", ips[3])
}

func TestExpandCIDR_IPv6_Small(t *testing.T) {
	// Test /126 subnet (4 addresses)
	ips := ExpandCIDR("2000::/126")
	require.Len(t, ips, 4)
	require.Equal(t, "2000::", ips[0])
	require.Equal(t, "2000::1", ips[1])
	require.Equal(t, "2000::2", ips[2])
	require.Equal(t, "2000::3", ips[3])
}

func TestExpandCIDR_InvalidCIDR(t *testing.T) {
	// Should return the input as-is if not valid CIDR
	ips := ExpandCIDR("192.168.1.1")
	require.Len(t, ips, 1)
	require.Equal(t, "192.168.1.1", ips[0])
}

func TestExpandCIDR_LargeSubnet(t *testing.T) {
	// Test /24 subnet (256 addresses) - just check count
	ips := ExpandCIDR("10.0.0.0/24")
	require.Len(t, ips, 256)
	require.Equal(t, "10.0.0.0", ips[0])
	require.Equal(t, "10.0.0.255", ips[255])
}

func TestExpandCIDR_Basic(t *testing.T) {
	// Basic functionality test for CIDR expansion
	got := ExpandCIDR("172.20.29.5/30")
	require.Len(t, got, 4, "expected 4 addresses for /30 subnet")
	// Ensure first and last addresses are non-empty
	require.NotEmpty(t, got[0], "first IP should not be empty")
	require.NotEmpty(t, got[len(got)-1], "last IP should not be empty")
}

func TestScapyASTParser_CIDR(t *testing.T) {
	// This test verifies that the Python parser correctly marks CIDR notation
	// We can't run Python from Go test, but we document the expected behavior:
	//
	// Input: IP(dst="172.20.29.5/30")
	// Expected output in IR JSON:
	// {
	//   "type": "IP",
	//   "params": {
	//     "dst": "172.20.29.5/30",
	//     "_special": {
	//       "dst": {
	//         "type": "cidr_expansion",
	//         "cidr": "172.20.29.5/30"
	//       }
	//     }
	//   }
	// }

	t.Skip("Python parser test - run manually with scapy_ast_parser.py")
}
