package functional

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func restoreBaselinePipelineCommands(fw *framework.TestFramework) []string {
	p := fw.Paths
	return []string{
		p.CLI("yanet-cli-function") + " update --name=virt --chains chain0:10=forward:forward0",
		p.CLI("yanet-cli-function") + " update --name=test --chains chain2:1=forward:forward0,route:route0",
		p.CLI("yanet-cli-pipeline") + " update --name=bootstrap --functions virt",
		p.CLI("yanet-cli-pipeline") + " update --name=test --functions test",
		p.CLI("yanet-cli-pipeline") + " update --name=dummy",
		p.CLI("yanet-cli-device-plain") + " update --name=01:00.0 --input test:1 --output dummy:1",
		p.CLI("yanet-cli-device-plain") + " update --name=virtio_user_kni0 --input bootstrap:1 --output dummy:1",
	}
}

// TestNoPipelines checks that no packet will be forwarded through device if
// pipeline assignment map is zero. Each run gets a clean isolated VM so that
// pipeline bindings from other tests do not leak in.
func TestNoPipelines(t *testing.T) {
	t.Parallel()
	runner := newBootedRunner(t)
	runner.RunBooted("Isolated_No_Pipelines", func(fw *framework.TestFramework, t *testing.T) {
		runNoPipelinesTest(fw, t)
	})
}

func runNoPipelinesTest(fw *framework.TestFramework, t *testing.T) {
	fw.Run("Configure_Device_No_Pipelines", func(fw *framework.TestFramework, t *testing.T) {
		p := fw.Paths
		commands := []string{
			// Clear both device assignment maps entirely. Unlike dummy pipeline
			// bindings, an empty input/output mapping guarantees packets have no
			// processing path and keeps the test aligned with its original intent.
			p.CLI("yanet-cli-device-plain") + " update --name=virtio_user_kni0",
			p.CLI("yanet-cli-device-plain") + " update --name=01:00.0",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure device with empty pipelines")
	})

	fw.Run("Test_Packet_Dropped", func(fw *framework.TestFramework, t *testing.T) {
		// Use SrcMAC for both src and dst -- framework skips packets
		// with MAC not equal to the framework one.
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("10.0.0.1"),
			net.ParseIP("10.0.0.2"),
			make([]byte, 1000),
			&framework.TCPPacketOpts{
				DstMAC: framework.SrcMAC,
			},
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 200*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr net.Error
		require.ErrorAs(t, err, &netErr, "Error should be a net.Error")
		require.True(t, netErr.Timeout(), "Error should be a timeout")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Packet should be dropped - no pipelines")
	})

	fw.Run("Restore_Baseline_Device_Pipelines", func(fw *framework.TestFramework, t *testing.T) {
		_, err := fw.ExecuteCommands(restoreBaselinePipelineCommands(fw)...)
		require.NoError(t, err, "Failed to restore baseline device and pipeline bindings")
	})
}
