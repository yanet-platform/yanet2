package functional

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func TestBalancer(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testBalancer(t, fw)
	})
}

func testBalancer(t *testing.T, fw *framework.TestFramework) {

	fw.Run("Configure_Balancer_Module", func(fw *framework.TestFramework, t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			// Configure module
			framework.CLIBalancer + " update --name balancer0 --config /mnt/yanet2/balancer.yaml",

			framework.CLIBalancer + " config --name balancer0",

			framework.CLIFunction + " update --name=test --chains ch0:2=balancer:balancer0,route:route0",

			framework.CLIPipeline + " update --name=test --functions test",

			framework.CLIDevicePlain + " update --name=01:00.0 --input test:1 --output dummy:1",

			framework.CLIBalancer + " stats --name=balancer0 --device=01:00.0 --pipeline=test --function=test --chain=ch0",

			framework.CLIBalancer + " reals enable --name=balancer0 --vs 192.0.2.1:80/tcp --reals 10.1.1.1",
			framework.CLIBalancer + " reals flush --name=balancer0",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure balancer module")
	})

	fw.Run("Test_IPv4_Packet", func(fw *framework.TestFramework, t *testing.T) {
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.168.2.2"),
			net.ParseIP("192.0.2.1"),
			[]byte("test balancer"),
			&framework.TCPPacketOpts{
				SrcPort: 12345,
				DstPort: 80,
				SYN:     true,
			},
		)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		t.Log("inputPacket", inputPacket)
		t.Log("outputPacket", outputPacket)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.True(t, outputPacket.IsTunneled, "Output packet should be tunneled")
		require.True(t, outputPacket.DstIP.String() == "10.1.1.1")
		require.Equal(t, outputPacket.InnerPacket.SrcIP.String(), "192.168.2.2")
		require.True(t, outputPacket.InnerPacket.IsIPv4)
	})
}
