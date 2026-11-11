package functional

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// Returns selected device counters summed across worker instances.
func deviceCounterValues(
	t *testing.T,
	testFramework *framework.TestFramework,
	device string,
	names ...string,
) map[string][]uint64 {
	t.Helper()

	var command strings.Builder
	command.WriteString(testFramework.Paths.CLI("yanet-cli-counters"))
	command.WriteString(" --format json --device " + device + " --kind device")
	for _, name := range names {
		command.WriteString(" " + name)
	}
	output, err := testFramework.ExecuteCommand(command.String())
	require.NoError(t, err)

	var response struct {
		Groups []struct {
			Counters []struct {
				Name      string `json:"name"`
				Instances []struct {
					Values []uint64 `json:"values"`
				} `json:"instances"`
			} `json:"counters"`
		} `json:"groups"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &response))

	valuesByName := map[string][]uint64{}
	for _, group := range response.Groups {
		for _, counter := range group.Counters {
			values := valuesByName[counter.Name]
			for _, instance := range counter.Instances {
				if len(values) < len(instance.Values) {
					missing := len(instance.Values) - len(values)
					values = append(values, make([]uint64, missing)...)
				}
				for idx, value := range instance.Values {
					values[idx] += value
				}
			}
			valuesByName[counter.Name] = values
		}
	}
	for _, name := range names {
		require.Contains(t, valuesByName, name)
	}
	return valuesByName
}

// Asserts an exact packet-and-byte delta for one counter.
func requireCounterDelta(
	t *testing.T,
	before, after, expected []uint64,
) {
	t.Helper()
	require.Len(t, before, len(expected))
	require.Len(t, after, len(expected))
	for idx := range expected {
		require.Equal(t, expected[idx], after[idx]-before[idx])
	}
}

// createICMPPacket creates a simple ICMP echo request packet for testing
func createICMPPacket(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip4, &icmp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createICMPv6Packet creates a simple ICMPv6 echo request packet for testing
func createICMPv6Packet(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	err := icmp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip6, &icmp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// TestForward tests basic forward module functionality including L2 forwarding
// and ICMP echo through the kni0 kernel interface.
func TestForward(t *testing.T) {
	withBootedVM(t, func(fw *framework.TestFramework) {
		testForward(t, fw)
	})
}

func testForward(t *testing.T, fw *framework.TestFramework) {
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Configure_Forward_Module", func(fw *framework.TestFramework, t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=forward:forward0,route:route0",
			// Configure pipelines
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	fw.Run("Test_Forwarding", func(fw *framework.TestFramework, t *testing.T) {
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
			net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
			[]byte("forward test"),
			nil,
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify packet was forwarded with preserved addresses
		assert.Equal(t, "192.0.2.1", outputPacket.SrcIP.String(), "Source IP should be preserved")
		assert.Equal(t, "192.0.2.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
	})

	fw.Run("Test_ICMP4_Echo", func(fw *framework.TestFramework, t *testing.T) {
		packet := createICMPPacket(
			net.ParseIP(framework.VMIPv4Gateway),
			net.ParseIP(framework.VMIPv4Host),
			[]byte("icmp test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, framework.VMIPv4Host, outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, framework.VMIPv4Gateway, outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})

	fw.Run("Test_ICMP6_Echo", func(fw *framework.TestFramework, t *testing.T) {
		// Test ICMPv6 echo request to VMIPv6Host
		packet := createICMPv6Packet(
			net.ParseIP(framework.VMIPv6Gateway), // src IP
			net.ParseIP(framework.VMIPv6Host),    // dst IP (in L3 forwarding table)
			[]byte("icmpv6 test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMPv6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify that we received an ICMPv6 echo reply
		// For ICMPv6 echo reply, the type should be 129 (EchoReply)
		// and the addresses should be swapped
		assert.Equal(t, framework.VMIPv6Host, outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, framework.VMIPv6Gateway, outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})

	fw.Run("Test_Batch", func(fw *framework.TestFramework, t *testing.T) {
		// Send two packets, the first one is passed through forward
		// module whereas the second should be routed to a kernel and
		// responded with an ICMP
		packets := [][]byte{
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
		}

		outputPackets, err := fw.SendPacketsAndParseAll(0, 0, packets, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send batch")

		assert.Equal(t, 8, len(outputPackets), "eight packets expected")
		cntICMP := 0
		for _, pack := range outputPackets {
			if framework.VMIPv4Host == pack.SrcIP.String() &&
				framework.VMIPv4Gateway == pack.DstIP.String() {
				cntICMP++
			}
		}

		assert.Equal(t, 4, cntICMP, "four ICMP replies are expected")
	})

}

// counterDelta subtracts two counter snapshots of packet and byte values
// summed across worker instances, to log traffic inside a counter window.
func counterDelta(before, after []uint64) []uint64 {
	delta := make([]uint64, 0, len(after))
	for idx, value := range after {
		delta = append(delta, value-before[idx])
	}
	return delta
}

// Test_PacketRecircLimit_FromDataplaneConfig verifies that the
// packet-recirculation limit from the dataplane YAML reaches live module
// execution and that kernel traffic stays off the physical counters.
func Test_PacketRecircLimit_FromDataplaneConfig(t *testing.T) {
	withBootedVM(t, func(testFramework *framework.TestFramework) {
		const (
			device    = "01:00.0"
			kniDevice = "virtio_user_kni0"
		)

		// The first four payload bytes become the echo identifier and
		// sequence, so the answer can be matched byte for byte.
		echoPayload := []byte{0xca, 0xfe, 0x00, 0x07, 'r', 'e', 'c', 'i', 'r', 'c', '6'}
		echoRequest := createICMPv6Packet(
			net.ParseIP(framework.VMIPv6Gateway),
			net.ParseIP(framework.VMIPv6Host),
			echoPayload,
		)

		controlReply, err := testFramework.SendPacketAndCapture(
			0, 0, echoRequest, 500*time.Millisecond,
		)
		require.NoError(t, err, "echo path must answer before the mapping")
		reply := gopacket.NewPacket(controlReply, layers.LayerTypeEthernet, gopacket.Default)
		ip6, ok := reply.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
		require.True(t, ok, "reply must carry an IPv6 layer")
		require.Equal(t, framework.VMIPv6Host, ip6.SrcIP.String(), "reply source must be the request destination")
		require.Equal(t, framework.VMIPv6Gateway, ip6.DstIP.String(), "reply destination must be the request source")
		icmp6, ok := reply.Layer(layers.LayerTypeICMPv6).(*layers.ICMPv6)
		require.True(t, ok, "reply must carry an ICMPv6 layer")
		require.Equal(
			t,
			layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoReply, 0),
			icmp6.TypeCode,
			"reply must be an ICMPv6 echo reply",
		)
		require.Equal(t, echoPayload, icmp6.Payload, "reply must echo the request payload")

		require.NoError(t, testFramework.CreateConfigFile("recirc-forward.yaml", `
rules:
  - action:
      target: "01:00.0"
      mode: OUT
      counter: recirc
    devices:
      - name: "01:00.0"
    vlan_ranges:
      - from: 0
        to: 4095
    sources4:
      - "0.0.0.0/0"
    destinations4:
      - "0.0.0.0/0"
`))

		paths := testFramework.Paths
		_, err = testFramework.ExecuteCommands(
			// The omitted input drops kernel ingress at the
			// interface; the output stays to the kernel.
			paths.CLI("yanet-cli-device-plain")+
				" update --name=virtio_user_kni0 --output dummy:1",
			paths.CLI("yanet-cli-forward")+
				" update --name=recirc "+
				"/mnt/config/recirc-forward.yaml",
			paths.CLI("yanet-cli-function")+
				" update --name=recirc --chains recirc:1=forward:recirc",
			paths.CLI("yanet-cli-pipeline")+
				" update --name=recirc --functions recirc",
			paths.CLI("yanet-cli-device-plain")+
				" update --name="+device+
				" --input test:1 --output recirc:1",
		)
		require.NoError(t, err)

		physicalBefore := deviceCounterValues(
			t, testFramework, device, "output_rx", "output_recirc_drop",
		)
		kniBefore := deviceCounterValues(
			t, testFramework, kniDevice, "input_rx", "input_drop",
		)

		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.0.2.1"),
			net.ParseIP("198.51.100.1"),
			[]byte("recirculation limit"),
			nil,
		)
		input, output, err := testFramework.SendPacketAndParse(
			0, 0, packet, 200*time.Millisecond,
		)
		require.Error(t, err)
		require.NotNil(t, input)
		require.Nil(t, output)

		// The same echo request inside the counter window. The capture
		// outcome is stored and asserted only after the exact counter
		// checks, so an unisolated run fails on the counters first.
		_, echoErr := testFramework.SendPacketAndCapture(
			0, 0, echoRequest, 200*time.Millisecond,
		)

		physicalAfter := deviceCounterValues(
			t, testFramework, device, "output_rx", "output_recirc_drop",
		)
		kniAfter := deviceCounterValues(
			t, testFramework, kniDevice, "input_rx", "input_drop",
		)

		for _, name := range []string{"output_rx", "output_recirc_drop"} {
			t.Logf("physical %s delta %v", name, counterDelta(physicalBefore[name], physicalAfter[name]))
		}
		for _, name := range []string{"input_rx", "input_drop"} {
			t.Logf("kni %s delta %v", name, counterDelta(kniBefore[name], kniAfter[name]))
		}

		packetSize := uint64(len(packet))
		recircLimit := uint64(testPacketRecircLimit)
		requireCounterDelta(
			t,
			physicalBefore["output_rx"],
			physicalAfter["output_rx"],
			[]uint64{recircLimit, recircLimit * packetSize},
		)
		requireCounterDelta(
			t,
			physicalBefore["output_recirc_drop"],
			physicalAfter["output_recirc_drop"],
			[]uint64{1, packetSize},
		)

		require.ErrorIs(t, echoErr, framework.ErrCaptureTimeout)

		// The kernel interface counters aggregate all kernel traffic
		// and cannot identify the probe answer.
		for _, name := range []string{"input_rx", "input_drop"} {
			require.Positive(
				t,
				kniAfter[name][0]-kniBefore[name][0],
				"kernel-interface %s must count kernel traffic", name,
			)
		}
	})
}
