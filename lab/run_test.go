package lab_test

import (
	"errors"
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
)

type manifestRuntime struct {
	Actual     [][]byte
	CaptureErr error
	Commands   []string
	Unfiltered bool
}

func (m *manifestRuntime) CommonConfigCommands() []string { return nil }
func (m *manifestRuntime) ExecuteCommand(command string) (string, error) {
	m.Commands = append(m.Commands, command)
	return "", nil
}
func (m *manifestRuntime) ExecuteCommandWithTimeout(string, time.Duration) (string, error) {
	return "", nil
}
func (m *manifestRuntime) ExecuteCommands(...string) ([]string, error) { return nil, nil }
func (m *manifestRuntime) ResetConnections()                           {}
func (m *manifestRuntime) RestoreClean(string) error                   { return nil }
func (m *manifestRuntime) SendPacketAndCaptureAllUnfiltered(int, int, []byte, time.Duration) ([][]byte, error) {
	m.Unfiltered = true
	return m.Actual, m.CaptureErr
}
func (m *manifestRuntime) StartYANET(string, string) error          { return nil }
func (m *manifestRuntime) WaitForDatapathReady(time.Duration) error { return nil }

func TestRunManifestEvaluatesExpectedDrop(t *testing.T) {
	tests := []struct {
		name       string
		actual     [][]byte
		captureErr error
		wantOK     bool
		wantError  string
	}{
		{name: "no packet", wantOK: true},
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
			actual:    [][]byte{{1, 2, 3}},
			wantError: "expected packet to be dropped, but captured 1",
		},
		{
			name:      "multiple captured packets",
			actual:    [][]byte{{1, 2, 3}, {4, 5, 6}},
			wantError: "expected packet to be dropped, but captured 2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestPath := writeDropManifest(t)
			runtime := &manifestRuntime{Actual: test.actual, CaptureErr: test.captureErr}
			report := lab.RunManifest(runtime, manifestPath)

			require.Equal(t, test.wantOK, report.Success)
			require.True(t, runtime.Unfiltered)
			require.Len(t, report.Results, 1)
			require.Equal(t, "probe", report.Results[0].Kind)
			require.Equal(t, test.wantError, report.Results[0].Error)
		})
	}
}

func TestRunManifestTransfersFilesInBoundedCommands(t *testing.T) {
	directory := t.TempDir()
	fixture := filepath.Join(directory, "fixture")
	if err := os.WriteFile(fixture, make([]byte, 769), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: files\nfiles:\n  - source: fixture\n    destination: /tmp/fixture\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{}
	report := lab.RunManifest(runtime, manifestPath)
	if !report.Success {
		t.Fatalf("report = %#v", report)
	}
	if len(runtime.Commands) != 4 {
		t.Fatalf("commands = %#v", runtime.Commands)
	}
	for _, command := range runtime.Commands {
		if len(command) > 800 {
			t.Fatalf("command is too long: %d", len(command))
		}
	}
}

func TestRunManifestIgnoresEthernetPadding(t *testing.T) {
	directory := t.TempDir()
	expected := []byte{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0,
		0x45, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 192, 0, 2, 1, 198, 51, 100, 1,
	}
	writePacket(t, filepath.Join(directory, "input.pcap"), expected)
	writePacket(t, filepath.Join(directory, "expected.pcap"), expected)
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: padding\nprobes:\n  - name: packet\n    ingress: 0\n    egress: 1\n    send: {pcap: input.pcap}\n    expect: {pcap: expected.pcap}\n"
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))

	runtime := &manifestRuntime{Actual: [][]byte{append(expected, 0, 0, 0, 0)}}
	report := lab.RunManifest(runtime, manifestPath)

	require.True(t, report.Success)
	require.True(t, report.Results[0].Success)
}

func TestRunManifestComparesMalformedIPFramesExactly(t *testing.T) {
	directory := t.TempDir()
	expected := []byte{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0,
		0x55, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 192, 0, 2, 1, 198, 51, 100, 1,
	}
	writePacket(t, filepath.Join(directory, "input.pcap"), expected)
	writePacket(t, filepath.Join(directory, "expected.pcap"), expected)
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: malformed\nprobes:\n  - name: packet\n    ingress: 0\n    egress: 1\n    send: {pcap: input.pcap}\n    expect: {pcap: expected.pcap}\n"
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))

	runtime := &manifestRuntime{Actual: [][]byte{append(expected, 0, 0, 0, 0)}}
	report := lab.RunManifest(runtime, manifestPath)

	require.False(t, report.Success)
	require.Contains(t, report.Results[0].Error, "packet mismatch")
}

func writeDropManifest(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	packetPath := filepath.Join(directory, "input.pcap")
	writePacket(t, packetPath, []byte{0, 1, 2, 3})

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

func writePacket(t *testing.T, path string, packet []byte) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := pcapgo.NewWriter(file)
	require.NoError(t, writer.WriteFileHeader(65535, layers.LinkTypeEthernet))
	require.NoError(t, writer.WritePacket(gopacket.CaptureInfo{
		Timestamp:     time.Now(),
		CaptureLength: len(packet),
		Length:        len(packet),
	}, packet))
	require.NoError(t, file.Close())
}
