package lab_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

type manifestRuntime struct {
	Actual     []byte
	CaptureErr error
}

func (m *manifestRuntime) CommonConfigCommands() []string { return nil }
func (m *manifestRuntime) ExecuteCommand(string) (string, error) {
	return "", nil
}
func (m *manifestRuntime) ExecuteCommandWithTimeout(string, time.Duration) (string, error) {
	return "", nil
}
func (m *manifestRuntime) ExecuteCommands(...string) ([]string, error) { return nil, nil }
func (m *manifestRuntime) ResetConnections()                           {}
func (m *manifestRuntime) RestoreClean(string) error                   { return nil }
func (m *manifestRuntime) SendPacketAndCapture(int, int, []byte, time.Duration) ([]byte, error) {
	return m.Actual, m.CaptureErr
}
func (m *manifestRuntime) StartYANET(string, string) error          { return nil }
func (m *manifestRuntime) WaitForDatapathReady(time.Duration) error { return nil }

func TestRunManifestEvaluatesExpectedDrop(t *testing.T) {
	tests := []struct {
		name       string
		actual     []byte
		captureErr error
		wantOK     bool
		wantError  string
	}{
		{name: "no packet", wantOK: true},
		{
			name:       "receive timeout",
			captureErr: fmt.Errorf("capture: %w", framework.ErrCaptureTimeout),
			wantOK:     true,
		},
		{
			name:       "connection failure",
			captureErr: errors.New("failed to connect to output socket"),
			wantError:  "failed to connect to output socket",
		},
		{
			name:       "send failure",
			captureErr: errors.New("failed to send packet"),
			wantError:  "failed to send packet",
		},
		{
			name:       "capture setup failure",
			captureErr: errors.New("failed to set read deadline"),
			wantError:  "failed to set read deadline",
		},
		{
			name:      "captured packet",
			actual:    []byte{1, 2, 3},
			wantError: "expected packet to be dropped, but one was captured",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestPath := writeDropManifest(t)
			runtime := &manifestRuntime{Actual: test.actual, CaptureErr: test.captureErr}
			report := lab.RunManifest(runtime, manifestPath)

			require.Equal(t, test.wantOK, report.Success)
			require.Len(t, report.Results, 1)
			require.Equal(t, "probe", report.Results[0].Kind)
			require.Equal(t, test.wantError, report.Results[0].Error)
		})
	}
}

func writeDropManifest(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	packetPath := filepath.Join(directory, "input.pcap")
	file, err := os.Create(packetPath)
	require.NoError(t, err)
	writer := pcapgo.NewWriter(file)
	require.NoError(t, writer.WriteFileHeader(65535, layers.LinkTypeEthernet))
	packet := []byte{0, 1, 2, 3}
	require.NoError(t, writer.WritePacket(gopacket.CaptureInfo{
		Timestamp:     time.Now(),
		CaptureLength: len(packet),
		Length:        len(packet),
	}, packet))
	require.NoError(t, file.Close())

	manifest := strings.Join([]string{
		"version: 1",
		"name: drop",
		"probes:",
		"  - name: packet",
		"    ingress: 0",
		"    egress: 1",
		"    send: {pcap: input.pcap}",
		"    expect: {drop: true}",
		"",
	}, "\n")
	manifestPath := filepath.Join(directory, "manifest.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))
	return manifestPath
}
