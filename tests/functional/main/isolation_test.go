package functional

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestIsolation verifies that socket connection reset prevents packet leakage
// between tests. The test sends a packet, intentionally does not read it,
// then resets the connection and confirms the buffered packet is gone.
func TestIsolation(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Configure_Forward_Module", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=forward:forward0,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	// Step 1: Send a packet and verify it arrives on the output interface.
	// Then send another packet but do NOT read it — it stays in the socket buffer.
	fw.Run("Step_1_Send_packet_and_leave_unread", func(fw *framework.F, t *testing.T) {
		inputClient, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, inputClient.Connect())

		outputClient, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, outputClient.Connect())

		// Send first packet and read it back to confirm the path works
		packet := createICMPPacket(
			net.ParseIP(framework.VMIPv4Gateway),
			net.ParseIP(framework.VMIPv4Host),
			[]byte("isolation-verify"),
		)

		require.NoError(t, inputClient.SendPacket(packet, ""))
		_, err = outputClient.ReceivePacket(500*time.Millisecond, "")
		require.NoError(t, err, "First packet should arrive successfully")

		// Send second packet but do NOT read it — it will be buffered
		require.NoError(t, inputClient.SendPacket(packet, ""))

		// Give the VM time to process and forward the packet to the output socket
		time.Sleep(100 * time.Millisecond)
	})

	// Step 2: Reset the connection and verify the buffered packet is gone.
	// This is the key isolation test — after ResetConnection(), the unread
	// packet from Step 1 must NOT be readable.
	fw.Run("Step_2_Verify_buffer_is_clean_after_reset", func(fw *framework.F, t *testing.T) {
		outputClient, err := fw.GetSocketClient(0)
		require.NoError(t, err)

		// Note: fw.Run() already called resetAllConnections() before this step,
		// so the connection is fresh. Attempt to read with a short timeout.
		require.NoError(t, outputClient.Connect())
		_, err = outputClient.ReceivePacket(100*time.Millisecond, "")
		assert.Error(t, err, "Should NOT receive any packet after connection reset — buffer must be clean")
	})

	// Step 3: Verify the connection still works after reset.
	// Send a new packet and confirm we can read it.
	fw.Run("Step_3_Verify_connection_works_after_reset", func(fw *framework.F, t *testing.T) {
		inputClient, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, inputClient.Connect())

		outputClient, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, outputClient.Connect())

		packet := createICMPPacket(
			net.ParseIP(framework.VMIPv4Gateway),
			net.ParseIP(framework.VMIPv4Host),
			[]byte("isolation-after-reset"),
		)

		require.NoError(t, inputClient.SendPacket(packet, ""))
		_, err = outputClient.ReceivePacket(500*time.Millisecond, "")
		require.NoError(t, err, "Packet should arrive after reset — connection must be functional")
	})
}
