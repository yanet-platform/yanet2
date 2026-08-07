package lab_test

import (
	"encoding/base64"
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
	Actual          [][]byte
	CaptureErr      error
	Commands        []string
	Unfiltered      bool
	failStep        int
	stepCount       int
	startYANETArgs  []string
	failRestore     bool
	failStartYANET  bool
	failWaitReady   bool
	waitReadyCalled bool
}

func (m *manifestRuntime) CommonConfigCommands() []string { return nil }
func (m *manifestRuntime) ExecuteCommand(command string) (string, error) {
	m.Commands = append(m.Commands, command)
	return "", nil
}
func (m *manifestRuntime) ExecuteCommandWithTimeout(string, time.Duration) (string, error) {
	m.stepCount++
	if m.stepCount >= m.failStep && m.failStep > 0 {
		return "", errors.New("step failed")
	}
	return "", nil
}
func (m *manifestRuntime) ExecuteCommands(...string) ([]string, error) { return nil, nil }
func (m *manifestRuntime) ResetConnections()                           {}
func (m *manifestRuntime) RestoreClean(string) error {
	if m.failRestore {
		return errors.New("restore failed")
	}
	return nil
}
func (m *manifestRuntime) SendPacketAndCaptureAllUnfiltered(int, int, []byte, time.Duration) ([][]byte, error) {
	m.Unfiltered = true
	return m.Actual, m.CaptureErr
}
func (m *manifestRuntime) StartYANET(dataplane, controlplane string) error {
	m.startYANETArgs = []string{dataplane, controlplane}
	if m.failStartYANET {
		return errors.New("start YANET failed")
	}
	return nil
}
func (m *manifestRuntime) WaitForDatapathReady(time.Duration) error {
	m.waitReadyCalled = true
	if m.failWaitReady {
		return errors.New("datapath not ready")
	}
	return nil
}

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
	want := make([]byte, 769)
	for idx := range want {
		want[idx] = byte(idx)
	}
	if err := os.WriteFile(fixture, want, 0o600); err != nil {
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
	if len(runtime.Commands) < 2 {
		t.Fatalf("expected at least 2 commands, got %d: %#v", len(runtime.Commands), runtime.Commands)
	}
	for _, command := range runtime.Commands {
		if len(command) > 5600 {
			t.Fatalf("command is too long: %d", len(command))
		}
	}
	var transferred []byte
	for _, command := range runtime.Commands[1:] {
		encoded := strings.TrimPrefix(command, "echo '")
		encoded = strings.TrimSuffix(encoded, "' | base64 -d >> '/tmp/fixture'")
		chunk, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		transferred = append(transferred, chunk...)
	}
	require.Equal(t, want, transferred)
}

func TestRunManifestRejectsOversizedFiles(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "fixture"), make([]byte, 64*1024+1), 0o600))
	manifestPath := filepath.Join(directory, "manifest.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte("version: 1\nname: files\nfiles:\n  - source: fixture\n    destination: /tmp/fixture\n"), 0o600))

	runtime := &manifestRuntime{}
	report := lab.RunManifest(runtime, manifestPath)

	require.False(t, report.Success)
	require.Contains(t, report.Results[0].Error, "too large")
	require.Empty(t, runtime.Commands)
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

func TestRunManifestRejectsNonEthernetPCAP(t *testing.T) {
	directory := t.TempDir()
	writePacketWithLinkType(t, filepath.Join(directory, "input.pcap"), layers.LinkTypeIPv4, []byte{1, 2, 3})
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: link\nprobes:\n  - name: packet\n    ingress: 0\n    egress: 1\n    send: {pcap: input.pcap}\n    expect: {drop: true}\n"
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))

	report := lab.RunManifest(&manifestRuntime{}, manifestPath)

	require.False(t, report.Success)
	require.Contains(t, report.Results[0].Error, "want Ethernet")
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
	writePacketWithLinkType(t, path, layers.LinkTypeEthernet, packet)
}

func TestShellJoinQuotesArguments(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"echo", "hello"}, "'echo' 'hello'"},
		{[]string{"echo", "it's fine"}, "'echo' 'it'\"'\"'s fine'"},
		{[]string{"echo", "$HOME"}, "'echo' '$HOME'"},
		{[]string{"echo", "; rm -rf /"}, "'echo' '; rm -rf /'"},
		{[]string{"echo", "`whoami`"}, "'echo' '`whoami`'"},
		{[]string{"cat", "/tmp/yanet/config.yaml"}, "'cat' '/tmp/yanet/config.yaml'"},
		{[]string{"echo", ""}, "'echo' ''"},
	}
	for _, tc := range cases {
		got := lab.ShellJoin(tc.argv)
		if got != tc.want {
			t.Errorf("ShellJoin(%v) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestWithoutEthernetPadding(t *testing.T) {
	// Ethernet header: dst(6) + src(6) + ethertype(2) = 14 bytes.
	buildIPv4 := func(vlanTags int) []byte {
		header := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
		for range vlanTags {
			header = append(header, 0x81, 0x00, 0x00, 0x64)
		}
		header = append(header, 0x08, 0x00)
		// IPv4: version 4, IHL 5, total length 20.
		ip := []byte{0x45, 0x00, 0x00, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		return append(header, ip...)
	}
	buildIPv6 := func() []byte {
		header := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
		header = append(header, 0x86, 0xdd)
		// IPv6 header is 40 bytes; payload length 0.
		ip := make([]byte, 40)
		ip[0] = 0x60
		return append(header, ip...)
	}

	cases := []struct {
		name   string
		packet []byte
		want   int
	}{
		{name: "untagged ipv4", packet: buildIPv4(0), want: 34},
		{name: "single vlan ipv4", packet: buildIPv4(1), want: 38},
		{name: "stacked vlan ipv4", packet: buildIPv4(2), want: 42},
		{name: "ipv6", packet: buildIPv6(), want: 54},
		{name: "short packet", packet: []byte{0, 1}, want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lab.WithoutEthernetPadding(tc.packet)
			if len(got) != tc.want {
				t.Fatalf("WithoutEthernetPadding(%d bytes) = %d bytes, want %d", len(tc.packet), len(got), tc.want)
			}
		})
	}
}

func TestWithoutEthernetPaddingTrimsPadding(t *testing.T) {
	// IPv4 total length 20, then 40 bytes of trailing padding.
	packet := append([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0x00,
		0x45, 0x00, 0x00, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		make([]byte, 40)...)
	got := lab.WithoutEthernetPadding(packet)
	if len(got) != 34 {
		t.Fatalf("WithEthernetPadding with trailing padding = %d bytes, want 34", len(got))
	}
}

func TestRunManifestBootFailsOnRestoreError(t *testing.T) {
	directory := t.TempDir()
	dataplaneYAML := filepath.Join(directory, "dataplane.yaml")
	if err := os.WriteFile(dataplaneYAML, []byte("interfaces: []"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: boot-test\nboot:\n  dataplane: dataplane.yaml\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{failRestore: true}
	report := lab.RunManifest(runtime, manifestPath)
	require.False(t, report.Success)
	require.Len(t, report.Results, 1)
	require.Equal(t, "boot", report.Results[0].Kind)
	require.Contains(t, report.Results[0].Error, "restore failed")
}

func writePacketWithLinkType(t *testing.T, path string, linkType layers.LinkType, packet []byte) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := pcapgo.NewWriter(file)
	require.NoError(t, writer.WriteFileHeader(65535, linkType))
	require.NoError(t, writer.WritePacket(gopacket.CaptureInfo{
		Timestamp:     time.Now(),
		CaptureLength: len(packet),
		Length:        len(packet),
	}, packet))
	require.NoError(t, file.Close())
}

func TestRunManifestBootCallsStartYANET(t *testing.T) {
	directory := t.TempDir()
	dataplaneYAML := filepath.Join(directory, "dataplane.yaml")
	controlplaneYAML := filepath.Join(directory, "controlplane.yaml")
	if err := os.WriteFile(dataplaneYAML, []byte("interfaces: []"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controlplaneYAML, []byte("route: {configs: {}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: boot-test\nboot:\n  dataplane: dataplane.yaml\n  controlplane: controlplane.yaml\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{}
	report := lab.RunManifest(runtime, manifestPath)
	if !report.Success {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Results) == 0 || report.Results[0].Kind != "boot" {
		t.Fatalf("expected boot result, got %#v", report.Results)
	}
	if len(runtime.startYANETArgs) != 2 {
		t.Fatalf("expected StartYANET to be called, got %v", runtime.startYANETArgs)
	}
	require.Equal(t, "interfaces: []", runtime.startYANETArgs[0], "dataplane config forwarded to StartYANET")
	require.Equal(t, "route: {configs: {}}", runtime.startYANETArgs[1], "controlplane config forwarded to StartYANET")
}

func TestRunManifestBootFailsOnStartYANETError(t *testing.T) {
	directory := t.TempDir()
	dataplaneYAML := filepath.Join(directory, "dataplane.yaml")
	if err := os.WriteFile(dataplaneYAML, []byte("interfaces: []"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: boot-test\nboot:\n  dataplane: dataplane.yaml\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{failStartYANET: true}
	report := lab.RunManifest(runtime, manifestPath)
	require.False(t, report.Success)
	require.Len(t, report.Results, 1)
	require.Equal(t, "boot", report.Results[0].Kind)
	require.Contains(t, report.Results[0].Error, "start YANET failed")
	require.False(t, runtime.waitReadyCalled, "WaitForDatapathReady must not be called when StartYANET fails")
}

func TestRunManifestBootFailsOnWaitForDatapath(t *testing.T) {
	directory := t.TempDir()
	dataplaneYAML := filepath.Join(directory, "dataplane.yaml")
	if err := os.WriteFile(dataplaneYAML, []byte("interfaces: []"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: boot-test\nboot:\n  dataplane: dataplane.yaml\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{failWaitReady: true}
	report := lab.RunManifest(runtime, manifestPath)
	require.False(t, report.Success)
	require.Len(t, report.Results, 1)
	require.Equal(t, "boot", report.Results[0].Kind)
	require.Contains(t, report.Results[0].Error, "datapath not ready")
}

func TestRunManifestStepsBreakOnFailure(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "manifest.yaml")
	manifest := "version: 1\nname: steps-test\nsteps:\n" +
		"  - name: first\n    argv: [\"true\"]\n" +
		"  - name: second\n    argv: [\"false\"]\n" +
		"  - name: third\n    argv: [\"true\"]\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &manifestRuntime{failStep: 2}
	report := lab.RunManifest(runtime, manifestPath)
	require.False(t, report.Success)
	require.Equal(t, 2, runtime.stepCount, "third step must not execute after second step fails")
}
