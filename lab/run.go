package lab

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// ManifestRuntime provides the VM operations needed to execute a manifest.
type ManifestRuntime interface {
	CommonConfigCommands() []string
	ExecuteCommand(string) (string, error)
	ExecuteCommandWithTimeout(string, time.Duration) (string, error)
	ExecuteCommands(...string) ([]string, error)
	ResetConnections()
	RestoreClean(string) error
	SendPacketAndCapture(int, int, []byte, time.Duration) ([]byte, error)
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
		data, readErr := os.ReadFile(filepath.Join(baseDir, file.Source))
		result := Result{Name: file.Source, Kind: "file"}
		if readErr == nil {
			command := "echo " + shellQuote(base64.StdEncoding.EncodeToString(data)) +
				" | base64 -d > " + shellQuote(file.Destination)
			_, readErr = runtime.ExecuteCommand(command)
		}
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
			output, stepErr := runtime.ExecuteCommandWithTimeout(shellJoin(step.Argv), timeout)
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
	actual, captureErr := runtime.SendPacketAndCapture(probe.Ingress, probe.Egress, packets[0], timeout)
	if probe.Expect.Drop {
		result.Success, result.Error = evaluateDropProbe(actual, captureErr)
	} else {
		expected, expectedErr := readPCAP(filepath.Join(baseDir, probe.Expect.PCAP))
		switch {
		case expectedErr != nil:
			result.Error = expectedErr.Error()
		case captureErr != nil:
			result.Error = captureErr.Error()
		case len(expected) != 1:
			result.Error = fmt.Sprintf("expect PCAP must contain exactly one packet, got %d", len(expected))
		case string(expected[0]) != string(actual):
			result.Error = fmt.Sprintf("packet mismatch\nexpected: %s\nactual:   %s", hex.EncodeToString(expected[0]), hex.EncodeToString(actual))
		default:
			result.Success = true
		}
	}
	result.Duration = time.Since(started)
	return result
}

func evaluateDropProbe(actual []byte, captureErr error) (bool, string) {
	switch {
	case captureErr == nil && len(actual) == 0:
		return true, ""
	case errors.Is(captureErr, framework.ErrCaptureTimeout):
		return true, ""
	case captureErr != nil:
		return false, captureErr.Error()
	default:
		return false, "expected packet to be dropped, but one was captured"
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
	var packets [][]byte
	for {
		data, _, readErr := reader.ReadPacketData()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read PCAP packet %s: %w", path, readErr)
		}
		packets = append(packets, append([]byte(nil), data...))
	}
	return packets, nil
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
