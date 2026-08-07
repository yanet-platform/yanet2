package lab

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

type Result struct {
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Success  bool          `json:"success"`
	Duration time.Duration `json:"duration"`
	Output   string        `json:"output,omitempty"`
	Error    string        `json:"error,omitempty"`
}

type RunReport struct {
	Manifest string    `json:"manifest"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Success  bool      `json:"success"`
	Results  []Result  `json:"results"`
}

const (
	maxManifestFileSize = 64 * 1024
	maxResultOutput     = 8 << 10 // 8 KiB per step/probe result
)

// ManifestRuntime provides the VM operations needed to execute a manifest.
type ManifestRuntime interface {
	CommonConfigCommands() []string
	ExecuteCommand(string) (string, error)
	ExecuteCommandWithTimeout(string, time.Duration) (string, error)
	ExecuteCommands(...string) ([]string, error)
	ResetConnections()
	RestoreClean(string) error
	SendPacketAndCaptureAllUnfiltered(int, int, []byte, time.Duration) ([][]byte, error)
	StartYANET(string, string) error
	WaitForDatapathReady(time.Duration) error
}

// RunManifest executes a validated lab manifest against runtime.
func RunManifest(runtime ManifestRuntime, path string) RunReport {
	report := RunReport{Manifest: path, Started: time.Now(), Success: true}
	manifest, err := LoadManifest(path)
	if err != nil {
		report.Success = false
		report.Results = append(report.Results, Result{Name: "validate", Kind: "manifest", Error: err.Error()})
		report.Finished = time.Now()
		return report
	}
	baseDir := filepath.Dir(path)
	if manifest.Boot.Dataplane != "" || manifest.Boot.Controlplane != "" {
		started := time.Now()
		result := Result{Name: "custom-config", Kind: "boot"}
		dataplane := framework.DataplaneConfig(framework.DataplaneOptions{})
		controlplane := framework.DefaultControlplaneConfig()
		if manifest.Boot.Dataplane != "" {
			data, readErr := os.ReadFile(filepath.Join(baseDir, manifest.Boot.Dataplane))
			if readErr != nil {
				result.Error = readErr.Error()
			} else {
				dataplane = string(data)
			}
		}
		if result.Error == "" && manifest.Boot.Controlplane != "" {
			data, readErr := os.ReadFile(filepath.Join(baseDir, manifest.Boot.Controlplane))
			if readErr != nil {
				result.Error = readErr.Error()
			} else {
				controlplane = string(data)
			}
		}
		if result.Error == "" {
			if bootErr := runtime.RestoreClean("preyanet"); bootErr != nil {
				result.Error = bootErr.Error()
			} else if bootErr := runtime.StartYANET(dataplane, controlplane); bootErr != nil {
				result.Error = bootErr.Error()
			} else if _, bootErr := runtime.ExecuteCommands(runtime.CommonConfigCommands()...); bootErr != nil {
				result.Error = bootErr.Error()
			} else {
				runtime.ResetConnections()
				if bootErr := runtime.WaitForDatapathReady(15 * time.Second); bootErr != nil {
					result.Error = bootErr.Error()
				}
			}
		}
		result.Success = result.Error == ""
		result.Duration = time.Since(started)
		report.Results = append(report.Results, result)
		report.Success = result.Success
	}

	for _, file := range manifest.Files {
		if !report.Success {
			break
		}
		started := time.Now()
		result := Result{Name: file.Source, Kind: "file"}
		readErr := transferFile(runtime, filepath.Join(baseDir, file.Source), file.Destination)
		result.Duration = time.Since(started)
		result.Success = readErr == nil
		if readErr != nil {
			result.Error = readErr.Error()
			report.Success = false
		}
		report.Results = append(report.Results, result)
		if readErr != nil {
			break
		}
	}

	if report.Success {
		for _, step := range manifest.Steps {
			started := time.Now()
			timeout := 30 * time.Second
			if step.Timeout != "" {
				timeout, _ = time.ParseDuration(step.Timeout)
			}
			output, stepErr := runtime.ExecuteCommandWithTimeout(ShellJoin(step.Argv), timeout)
			if len(output) > maxResultOutput {
				output = output[:maxResultOutput] + fmt.Sprintf("\n... truncated (%d bytes total)", len(output))
			}
			result := Result{Name: step.Name, Kind: "step", Success: stepErr == nil, Duration: time.Since(started), Output: output}
			if stepErr != nil {
				result.Error = stepErr.Error()
				report.Success = false
			}
			report.Results = append(report.Results, result)
			if stepErr != nil {
				break
			}
		}
	}

	if report.Success {
		for _, probe := range manifest.Probes {
			result := runProbe(runtime, baseDir, probe)
			report.Results = append(report.Results, result)
			if !result.Success {
				report.Success = false
				break
			}
		}
	}
	report.Finished = time.Now()
	return report
}

func transferFile(runtime ManifestRuntime, source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxManifestFileSize {
		return fmt.Errorf("manifest file %s is too large: %d bytes, maximum %d", source, info.Size(), maxManifestFileSize)
	}
	if _, err := runtime.ExecuteCommand(": > " + shellQuote(destination)); err != nil {
		return err
	}
	buffer := make([]byte, 4096)
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			encoded := base64.StdEncoding.EncodeToString(buffer[:count])
			command := "echo " + shellQuote(encoded) + " | base64 -d >> " + shellQuote(destination)
			if _, err := runtime.ExecuteCommand(command); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func runProbe(runtime ManifestRuntime, baseDir string, probe Probe) Result {
	started := time.Now()
	result := Result{Name: probe.Name, Kind: "probe"}
	packets, err := readPCAP(filepath.Join(baseDir, probe.Send.PCAP))
	if err != nil {
		result.Error = err.Error()
		result.Duration = time.Since(started)
		return result
	}
	if len(packets) != 1 {
		result.Error = fmt.Sprintf("send PCAP must contain exactly one packet, got %d", len(packets))
		result.Duration = time.Since(started)
		return result
	}
	timeout := 500 * time.Millisecond
	if probe.Timeout != "" {
		timeout, _ = time.ParseDuration(probe.Timeout)
	}
	actual, captureErr := runtime.SendPacketAndCaptureAllUnfiltered(probe.Ingress, probe.Egress, packets[0], timeout)
	if captureErr != nil {
		result.Error = captureErr.Error()
	} else if probe.Expect.Drop {
		if len(actual) == 0 {
			result.Success = true
		} else {
			result.Error = fmt.Sprintf("expected packet to be dropped, but captured %d", len(actual))
		}
	} else {
		expected, expectedErr := readPCAP(filepath.Join(baseDir, probe.Expect.PCAP))
		switch {
		case expectedErr != nil:
			result.Error = expectedErr.Error()
		case len(expected) != 1:
			result.Error = fmt.Sprintf("expect PCAP must contain exactly one packet, got %d", len(expected))
		case len(actual) != 1:
			result.Error = fmt.Sprintf("expected exactly one packet, got %d", len(actual))
		case !equalPacket(expected[0], actual[0]):
			result.Error = fmt.Sprintf("packet mismatch\nexpected: %s\nactual:   %s", hex.EncodeToString(expected[0]), hex.EncodeToString(actual[0]))
		default:
			result.Success = true
		}
	}
	result.Duration = time.Since(started)
	return result
}

func equalPacket(expected, actual []byte) bool {
	return bytes.Equal(WithoutEthernetPadding(expected), WithoutEthernetPadding(actual))
}

// WithoutEthernetPadding strips Ethernet padding from a packet, returning only
// the bytes up to and including the IP payload as indicated by the IP header
// length field. Returns the original packet if the header is too short.
func WithoutEthernetPadding(packet []byte) []byte {
	if len(packet) < 14 {
		return packet
	}
	typeOffset := 12
	for {
		if len(packet) < typeOffset+2 {
			return packet
		}
		etherType := binary.BigEndian.Uint16(packet[typeOffset:])
		if etherType != 0x8100 && etherType != 0x88a8 {
			networkOffset := typeOffset + 2
			switch etherType {
			case 0x0800:
				if len(packet) < networkOffset+4 {
					return packet
				}
				if packet[networkOffset]>>4 != 4 {
					return packet
				}
				headerLength := int(packet[networkOffset]&0x0f) * 4
				length := int(binary.BigEndian.Uint16(packet[networkOffset+2:]))
				if headerLength >= 20 && length >= headerLength && networkOffset+length <= len(packet) {
					return packet[:networkOffset+length]
				}
			case 0x86dd:
				if len(packet) < networkOffset+6 {
					return packet
				}
				if packet[networkOffset]>>4 != 6 {
					return packet
				}
				length := 40 + int(binary.BigEndian.Uint16(packet[networkOffset+4:]))
				if networkOffset+length <= len(packet) {
					return packet[:networkOffset+length]
				}
			}
			return packet
		}
		typeOffset += 4
	}
}

func readPCAP(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open PCAP %s: %w", path, err)
	}
	defer file.Close()
	reader, err := pcapgo.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("read PCAP header %s: %w", path, err)
	}
	if reader.LinkType() != layers.LinkTypeEthernet {
		return nil, fmt.Errorf("PCAP %s uses link type %s, want Ethernet", path, reader.LinkType())
	}
	packets := make([][]byte, 0, 2)
	for {
		data, _, readErr := reader.ReadPacketData()
		if errors.Is(readErr, io.EOF) {
			return packets, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("read PCAP packet %s: %w", path, readErr)
		}
		packets = append(packets, append([]byte(nil), data...))
	}
}

// ShellJoin quotes each argument with single-quote escaping and joins them
// with spaces, suitable for passing a command and arguments to a POSIX shell.
func ShellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
