package functional

import (
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// routeCfgName is the route module config used by the route tests.
const routeCfgName = "route-tfn0"

// routeEgressDevice is the dataplane egress device used by the route
// tests. It matches the "01:00.0" device declared in the test
// dataplane configuration in framework_test.go.
const routeEgressDevice = "01:00.0"

// createRouteTestPacket creates a TCP packet for route testing.
func createRouteTestPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}
	err := tcp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// applyFIB writes a FIB YAML file under /mnt/config and pushes it via
// "yanet-cli-route fib update" against the named route module config.
//
// The MAC pair is intentionally swapped relative to the framework's
// canonical SrcMAC/DstMAC so that egressing packets carry the host's
// expected MACs, matching the assertions in the route packet tests.
//
// An empty prefixes set produces an empty entries list, which the CLI
// treats as a full FIB clear.
func applyFIB(t *testing.T, fw *framework.F, cfgName, suffix string, prefixes ...string) {
	t.Helper()

	type fibNexthopYAML struct {
		DstMAC string `yaml:"dst_mac"`
		SrcMAC string `yaml:"src_mac"`
		Device string `yaml:"device"`
	}
	type fibEntryYAML struct {
		Prefix   string           `yaml:"prefix"`
		Nexthops []fibNexthopYAML `yaml:"nexthops"`
	}
	type fibConfigYAML struct {
		Entries []fibEntryYAML `yaml:"entries"`
	}

	cfg := fibConfigYAML{Entries: make([]fibEntryYAML, 0, len(prefixes))}
	for _, prefix := range prefixes {
		cfg.Entries = append(cfg.Entries, fibEntryYAML{
			Prefix: prefix,
			Nexthops: []fibNexthopYAML{{
				DstMAC: framework.SrcMAC,
				SrcMAC: framework.DstMAC,
				Device: routeEgressDevice,
			}},
		})
	}

	body, err := yaml.Marshal(&cfg)
	require.NoError(t, err, "failed to marshal FIB config")

	name := cfgName + "-" + suffix + ".yaml"
	require.NoError(t, fw.CreateConfigFile(name, string(body)),
		"failed to create FIB config file")

	cmd := framework.CLIRoute + " fib update --name=" + cfgName +
		" --rules /mnt/config/" + name
	_, err = fw.ExecuteCommand(cmd)
	require.NoError(t, err, "failed to update FIB")
}

// TestRoute tests route module functionality including static route insertion and deletion.
func TestRoute(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Setup_Route_Config", func(fw *framework.F, t *testing.T) {
		applyFIB(t, fw, routeCfgName, "setup", "10.0.0.0/24")
	})

	fw.Run("Configure_Route_Module", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=route:" + routeCfgName,
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure route module")
	})

	fw.Run("Test_Packet_Routing_With_Route", func(fw *framework.F, t *testing.T) {
		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"),
			net.ParseIP("10.0.0.10"),
			[]byte("route test"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		if outputPacket != nil {
			t.Logf("Packet routed: src=%s dst=%s", outputPacket.SrcIP, outputPacket.DstIP)
		}
	})

	fw.Run("Delete_Static_Route", func(fw *framework.F, t *testing.T) {
		// fib update is a full atomic replacement; an empty entry set
		// effectively removes all routes from the module.
		applyFIB(t, fw, routeCfgName, "clear")
		t.Logf("Successfully cleared route FIB")
	})

	fw.Run("Test_Packet_Without_Route", func(fw *framework.F, t *testing.T) {
		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"),
			net.ParseIP("172.16.0.10"),
			[]byte("no route test"),
		)

		inputPacket, outputPacket0, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr0 net.Error
		require.ErrorAs(t, err, &netErr0, "Error should be a net.Error")
		require.True(t, netErr0.Timeout(), "Error should be a timeout")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket0, "Packet should be dropped (no route) on interface 0")

		outputPacket1, err := fw.SendPacketAndCapture(0, 1, packet, 100*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr1 net.Error
		require.ErrorAs(t, err, &netErr1, "Error should be a net.Error")
		require.True(t, netErr1.Timeout(), "Error should be a timeout")
		require.Nil(t, outputPacket1, "Packet should be dropped (no route) on interface 1")

		t.Logf("Packet correctly dropped (no matching route)")
	})

	fw.Run("Test_Packet_With_Default_Route", func(fw *framework.F, t *testing.T) {
		applyFIB(t, fw, routeCfgName, "default", "0.0.0.0/0")

		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"),
			net.ParseIP("172.16.0.10"),
			[]byte("default route test"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")

		require.NotNil(t, outputPacket, "Packet should be routed via default route")
		require.Equal(t, "172.16.0.10", outputPacket.DstIP.String(), "Destination IP should be preserved")
		t.Logf("Packet correctly routed via default route: src=%s dst=%s", outputPacket.SrcIP, outputPacket.DstIP)

		applyFIB(t, fw, routeCfgName, "default-clear")
		t.Logf("Cleared default route")
	})

}
