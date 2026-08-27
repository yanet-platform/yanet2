package lab

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

const birdVersion = "2.15.1.1785924912.af804ec4-1"

const birdArchiveURL = "https://github.com/yanet-platform/bird/releases/download/v2.15.1-yanet.1/ubuntu-24.04-packages.tar.gz"

const birdArchiveSHA256 = "b9af7890d3a5a9ae2db5780861412d42de96b9e69f9d047631eb81f0d48582a2"

var operatorCLIs = []string{
	"yanet-cli-ready",
	"yanet-cli-operator-route",
	"yanet-cli-operator-neighbour",
	"yanet-cli-operator-pipeline",
}

var operatorArtifacts = []string{
	"build/operators/route/yanet-route-operator",
	"build/operators/forward/yanet-forward-operator",
	"build/operators/decap/yanet-decap-operator",
	"build/operators/pipeline/yanet-pipeline-operator",
	"build/operators/bird-adapter/yanet-bird-adapter",
}

// ScopeState is the readiness state of one Operator Profile scope.
type ScopeState string

// Operator Profile scope states reported by the status parser.
const (
	StateReady    ScopeState = "ready"
	StateNotReady ScopeState = "not_ready"
)

// ScopeResult is the outcome of one Operator Profile scope probe.
type ScopeResult struct {
	// Name identifies the scope.
	Name string `json:"name"`
	// State is ready when the scope's probe succeeded.
	State ScopeState `json:"state"`
	// Reason names the failure, empty while the scope is ready.
	Reason string `json:"reason,omitempty"`
}

// operatorScope is one named probe of the pinned Operator Profile (AD-11). The
// Command exits zero while the scope is ready in the guest and Reason names
// the failure the probe detects.
type operatorScope struct {
	Name    string
	Command string
	Reason  string
}

// operatorScopes is the shared Operator Profile check table (AD-11). Startup
// health, waiting, and status derive commands from it so the surfaces cannot
// diverge.
var operatorScopes = []operatorScope{
	{Name: "dataplane", Command: "pgrep -f '[y]anet-dataplane' >/dev/null", Reason: "yanet-dataplane process is not running"},
	{Name: "controlplane", Command: "pgrep -f '[y]anet-controlplane' >/dev/null", Reason: "yanet-controlplane process is not running"},
	{Name: "route-operator", Command: "pgrep -f '^/tmp/yanet/operators/yanet-route-operator ' >/dev/null", Reason: "yanet-route-operator process is not running"},
	{Name: "forward-operator", Command: "pgrep -f '^/tmp/yanet/operators/yanet-forward-operator ' >/dev/null", Reason: "yanet-forward-operator process is not running"},
	{Name: "decap-operator", Command: "pgrep -f '^/tmp/yanet/operators/yanet-decap-operator ' >/dev/null", Reason: "yanet-decap-operator process is not running"},
	{Name: "pipeline-operator", Command: "pgrep -f '^/tmp/yanet/operators/yanet-pipeline-operator ' >/dev/null", Reason: "yanet-pipeline-operator process is not running"},
	{Name: "bird", Command: "pgrep -x bird >/dev/null", Reason: "bird process is not running"},
	{Name: "bird-adapter", Command: "pgrep -f '^/tmp/yanet/operators/yanet-bird-adapter server ' >/dev/null", Reason: "yanet-bird-adapter server process is not running"},
	{Name: "ready-route", Command: "/tmp/yanet/cli/yanet-cli-ready route >/dev/null 2>&1", Reason: "route readiness service is not ready"},
	{Name: "ready-forward", Command: "/tmp/yanet/cli/yanet-cli-ready forward >/dev/null 2>&1", Reason: "forward readiness service is not ready"},
	{Name: "ready-decap", Command: "/tmp/yanet/cli/yanet-cli-ready decap >/dev/null 2>&1", Reason: "decap readiness service is not ready"},
	{Name: "ready-pipeline", Command: "/tmp/yanet/cli/yanet-cli-ready pipeline >/dev/null 2>&1", Reason: "pipeline readiness service is not ready"},
	{Name: "route0-session", Command: `sessions=$(/tmp/yanet/operators/yanet-bird-adapter list-sessions --server-config /tmp/yanet/config/operators/bird-adapter.yaml) && printf '%s\n' "$sessions" | awk '/^Name:/{route = $2 == "route0"} route && /^Connection:/{ready = $2 == "READY"} END{exit !ready}'`, Reason: "route0 adapter session is not connected"},
	{Name: "imported-route-v4", Command: `/tmp/yanet/cli/yanet-cli-operator-route show --name route0 --format json | grep -F "198.51.100.0/24" >/dev/null`, Reason: "imported route 198.51.100.0/24 is missing"},
	{Name: "imported-route-v6", Command: `/tmp/yanet/cli/yanet-cli-operator-route show --name route0 --format json | grep -F "2001:db8:100::/48" >/dev/null`, Reason: "imported route 2001:db8:100::/48 is missing"},
}

// ScopeNames returns the pinned Operator Profile scope names in execution
// order (AD-11).
func ScopeNames() []string {
	names := make([]string, len(operatorScopes))
	for idx, scope := range operatorScopes {
		names[idx] = scope.Name
	}
	return names
}

// operatorHealthCommand runs every scope probe and exits at the first failure,
// keeping the startup and wait loops fast. It is built from the shared table
// so startup and status cannot diverge (AD-11).
var operatorHealthCommand = buildOperatorHealthCommand()

func buildOperatorHealthCommand() string {
	commands := make([]string, len(operatorScopes))
	for idx, scope := range operatorScopes {
		commands[idx] = scope.Command
	}
	return strings.Join(commands, " && ")
}

// scopeStatusMarker is the fixed token prefixing every per-scope status line
// so the parser cannot confuse probe noise with the report.
const scopeStatusMarker = "YANET2_SCOPE"

// operatorStatusCommand runs every scope probe without short-circuiting and
// prints one marked line per scope, so a single guest command bounds the
// complete reporting pass.
var operatorStatusCommand = buildOperatorStatusCommand()

func buildOperatorStatusCommand() string {
	var builder strings.Builder
	for _, scope := range operatorScopes {
		fmt.Fprintf(&builder, "if %s; then printf '%s %s ready\\n'; else printf '%s %s not_ready\\n'; fi\n",
			scope.Command, scopeStatusMarker, scope.Name, scopeStatusMarker, scope.Name)
	}
	return builder.String()
}

// ParseScopeStatus parses the marked per-scope lines of the Operator Profile
// status command into one result per scope. It fails closed: a scope whose
// line is absent, truncated, duplicated, or otherwise malformed is reported
// NOT_READY with an explicit parse reason and is never assumed healthy.
func ParseScopeStatus(output string) []ScopeResult {
	matches := map[string][]string{}
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != scopeStatusMarker {
			continue
		}
		matches[fields[1]] = append(matches[fields[1]], line)
	}
	results := make([]ScopeResult, 0, len(operatorScopes))
	for _, scope := range operatorScopes {
		lines := matches[scope.Name]
		switch {
		case len(lines) == 0:
			results = append(results, ScopeResult{Name: scope.Name, State: StateNotReady, Reason: "status output missing"})
		case len(lines) > 1:
			results = append(results, ScopeResult{Name: scope.Name, State: StateNotReady, Reason: "duplicate status output"})
		default:
			results = append(results, parseScopeStatusLine(scope, lines[0]))
		}
	}
	return results
}

func parseScopeStatusLine(scope operatorScope, line string) ScopeResult {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return ScopeResult{Name: scope.Name, State: StateNotReady, Reason: "malformed status output"}
	}
	switch fields[2] {
	case "ready":
		return ScopeResult{Name: scope.Name, State: StateReady}
	case "not_ready":
		return ScopeResult{Name: scope.Name, State: StateNotReady, Reason: scope.Reason}
	case "degraded":
		return ScopeResult{Name: scope.Name, State: StateNotReady, Reason: "readiness degraded"}
	default:
		return ScopeResult{Name: scope.Name, State: StateNotReady, Reason: "malformed status output"}
	}
}

// CheckStatus runs every Operator Profile scope probe as one guest command and
// returns the parsed per-scope outcomes. It changes no Lab state and does not
// run the forwarding packet probe, which stays in startup health only.
func CheckStatus(fw *framework.TestFramework) ([]ScopeResult, error) {
	output, err := fw.ExecuteCommand(operatorStatusCommand)
	results := ParseScopeStatus(output)
	if err != nil {
		return results, fmt.Errorf("operator profile status: %w\n%s", err, TruncateOutput(output))
	}
	return results, nil
}

var forwardingProbe = []byte{
	0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5, 0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1, 0x08, 0x00,
	0x45, 0x00, 0x00, 0x26, 0x00, 0x01, 0x00, 0x00, 0x40, 0x11, 0x8e, 0x74, 0xc0, 0x00,
	0x02, 0x0a, 0xc6, 0x33, 0x64, 0x14, 0x30, 0x39, 0x1f, 0x90, 0x00, 0x12, 0xd8, 0xe6,
	0x79, 0x61, 0x6e, 0x65, 0x74, 0x32, 0x2d, 0x6c, 0x61, 0x62,
}

var forwardingExpected = []byte{
	0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1, 0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5, 0x08, 0x00,
	0x45, 0x00, 0x00, 0x26, 0x00, 0x01, 0x00, 0x00, 0x3f, 0x11, 0x8f, 0x74, 0xc0, 0x00,
	0x02, 0x0a, 0xc6, 0x33, 0x64, 0x14, 0x30, 0x39, 0x1f, 0x90, 0x00, 0x12, 0xd8, 0xe6,
	0x79, 0x61, 0x6e, 0x65, 0x74, 0x32, 0x2d, 0x6c, 0x61, 0x62,
}

// OperatorFingerprintFiles lists source files that invalidate the lab operator snapshot.
func OperatorFingerprintFiles() []string {
	files := append([]string{"lab/operators.go"}, operatorArtifacts...)
	for _, name := range operatorCLIs {
		files = append(files, "target/release/"+name)
	}
	return files
}

// RequiredArtifacts lists host files needed to prepare the operator baseline.
func RequiredArtifacts(root string) []string {
	paths := []string{
		filepath.Join(root, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(root, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(root, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
	}
	for _, path := range operatorArtifacts {
		if filepath.IsAbs(path) {
			paths = append(paths, path)
		} else {
			paths = append(paths, filepath.Join(root, path))
		}
	}
	for _, name := range framework.CLIBinaryNames {
		paths = append(paths, filepath.Join(root, "target", "release", name))
	}
	for _, name := range operatorCLIs {
		paths = append(paths, filepath.Join(root, "target", "release", name))
	}
	return paths
}

// PrepareOperators stages the lab-only operator and BIRD artifacts in guest tmpfs.
func PrepareOperators(fw *framework.TestFramework) error {
	if _, err := fw.ExecuteCommand("mkdir -p /tmp/yanet/operators /tmp/yanet/config/operators /tmp/yanet/bird /tmp/yanet/logs /tmp/yanet/run"); err != nil {
		return err
	}
	copyCommands := []string{
		"cp /mnt/build/operators/route/yanet-route-operator /tmp/yanet/operators/",
		"cp /mnt/build/operators/forward/yanet-forward-operator /tmp/yanet/operators/",
		"cp /mnt/build/operators/decap/yanet-decap-operator /tmp/yanet/operators/",
		"cp /mnt/build/operators/pipeline/yanet-pipeline-operator /tmp/yanet/operators/",
		"cp /mnt/build/operators/bird-adapter/yanet-bird-adapter /tmp/yanet/operators/",
	}
	for _, name := range operatorCLIs {
		copyCommands = append(copyCommands, "cp /mnt/target/release/"+name+" /tmp/yanet/cli/")
	}
	copyCommands = append(copyCommands, "chmod +x /tmp/yanet/operators/* /tmp/yanet/cli/yanet-cli-operator-* /tmp/yanet/cli/yanet-cli-ready")
	for _, command := range copyCommands {
		if _, err := fw.ExecuteCommandWithTimeout(command, time.Minute); err != nil {
			return fmt.Errorf("stage operator artifact: %w", err)
		}
	}
	for path, contents := range operatorFiles {
		if err := fw.WriteGuestFile(path, contents); err != nil {
			return err
		}
	}
	installBird := fmt.Sprintf("python3 -c \"import hashlib,sys,urllib.request; u='%s'; p='/tmp/yanet/bird/bird.tar.gz'; urllib.request.urlretrieve(u,p); h=hashlib.sha256(open(p,'rb').read()).hexdigest(); print(h); sys.exit(h != '%s')\" && tar -xzf /tmp/yanet/bird/bird.tar.gz -C /tmp/yanet/bird && dpkg -i /tmp/yanet/bird/ubuntu-24.04/yanet-bird2_%s/yanet-bird2_%s_amd64.deb && { systemctl stop bird bird6 2>/dev/null || true; pkill bird 2>/dev/null || true; }", birdArchiveURL, birdArchiveSHA256, birdVersion, birdVersion)
	if output, err := fw.ExecuteCommandWithTimeout(installBird, 3*time.Minute); err != nil {
		return fmt.Errorf("install pinned BIRD %s: %w\n%s", birdVersion, err, output)
	}
	return nil
}

// StartOperators launches the complete operator-owned lab configuration.
func StartOperators(fw *framework.TestFramework) error {
	commands := []string{
		"ip addr replace 203.0.113.14/24 dev kni0",
		"ip addr replace 2001:db8::14/64 dev kni0",
		"ip nei replace 203.0.113.1 lladdr 52:54:00:6b:ff:a1 dev kni0",
		"ip nei replace 2001:db8::1 lladdr 52:54:00:6b:ff:a1 dev kni0",
		"bash -c 'nohup /tmp/yanet/operators/yanet-route-operator -c /tmp/yanet/config/operators/route.yaml > /tmp/yanet/logs/yanet-route-operator.log 2>&1 &'",
		"bash -c 'nohup /tmp/yanet/operators/yanet-forward-operator -c /tmp/yanet/config/operators/forward.yaml > /tmp/yanet/logs/yanet-forward-operator.log 2>&1 &'",
		"bash -c 'nohup /tmp/yanet/operators/yanet-decap-operator -c /tmp/yanet/config/operators/decap.yaml > /tmp/yanet/logs/yanet-decap-operator.log 2>&1 &'",
		"bash -c 'nohup bird -c /tmp/yanet/config/operators/bird.conf > /tmp/yanet/logs/bird.log 2>&1 &'",
		"bash -c 'nohup /tmp/yanet/operators/yanet-bird-adapter server -c /tmp/yanet/config/operators/bird-adapter.yaml > /tmp/yanet/logs/yanet-bird-adapter.log 2>&1 &'",
	}
	for _, command := range commands {
		if _, err := fw.ExecuteCommand(command); err != nil {
			return err
		}
	}
	if _, err := fw.ExecuteCommandWithTimeout("for n in $(seq 1 60); do test -S /tmp/yanet/bird/master4.sock && test -S /tmp/yanet/bird/master6.sock && break; sleep 1; done; test -S /tmp/yanet/bird/master4.sock && test -S /tmp/yanet/bird/master6.sock", time.Minute); err != nil {
		output, _ := fw.ExecuteCommand("cat /tmp/yanet/logs/bird.log")
		return fmt.Errorf("wait for BIRD export sockets: %w\n%s", err, output)
	}
	adapterClient := "bash /tmp/yanet/operators/configure-bird.sh"
	if _, err := fw.ExecuteCommand("sleep 1"); err != nil {
		return err
	}
	if output, err := fw.ExecuteCommand(adapterClient); err != nil {
		return fmt.Errorf("configure BIRD adapter: %w\n%s", err, output)
	}
	if _, err := fw.ExecuteCommand("/tmp/yanet/cli/yanet-cli-function update --name=fn:lab --chains default:1=forward:forward0"); err != nil {
		return fmt.Errorf("create lab extension function: %w", err)
	}
	if _, err := fw.ExecuteCommand("bash -c 'nohup /tmp/yanet/operators/yanet-pipeline-operator -c /tmp/yanet/config/operators/pipeline.yaml > /tmp/yanet/logs/yanet-pipeline-operator.log 2>&1 &'"); err != nil {
		return err
	}
	return WaitOperators(fw)
}

// CheckOperators verifies the complete operator-owned lab profile.
func CheckOperators(fw *framework.TestFramework) error {
	output, err := fw.ExecuteCommand(operatorHealthCommand)
	if err != nil {
		return fmt.Errorf("operator profile is not ready: %w\n%s", err, output)
	}
	packets, err := fw.SendPacketAndCaptureAllUnfiltered(0, 0, forwardingProbe, time.Second)
	if err != nil {
		return fmt.Errorf("operator forwarding probe: %w", err)
	}
	if len(packets) != 1 || !MatchesForwardingProbe(packets[0]) {
		return fmt.Errorf("operator forwarding probe returned %d unexpected packets", len(packets))
	}
	return nil
}

func MatchesForwardingProbe(packet []byte) bool {
	stripped := WithoutEthernetPadding(packet)
	return bytes.Equal(stripped, forwardingExpected)
}

// ForwardingExpectedBytes returns a copy of the canonical operator forwarding
// probe packet bytes. The returned slice is a copy; callers may freely mutate
// it. The underlying fixture is private to prevent accidental shared state.
func ForwardingExpectedBytes() []byte {
	return append([]byte(nil), forwardingExpected...)
}

// WaitOperators waits for the complete operator-owned lab profile.
func WaitOperators(fw *framework.TestFramework) error {
	command := "ready=false; for attempt in $(seq 1 10); do timeout 3s sh -c " + shellQuote(operatorHealthCommand) + " && ready=true && break; sleep 1; done; $ready"
	if output, err := fw.ExecuteCommandWithTimeout(command, 50*time.Second); err != nil {
		logs, _ := fw.ExecuteCommand("pgrep -af 'bird|yanet-.*-operator'; /tmp/yanet/cli/yanet-cli-ready --all; /tmp/yanet/operators/yanet-bird-adapter list-sessions --server-config /tmp/yanet/config/operators/bird-adapter.yaml; /tmp/yanet/cli/yanet-cli-operator-route show --name route0 --format json; for file in /tmp/yanet/logs/bird.log /tmp/yanet/logs/yanet-bird-adapter.log /tmp/yanet/logs/yanet-route-operator.log /tmp/yanet/logs/yanet-forward-operator.log /tmp/yanet/logs/yanet-decap-operator.log /tmp/yanet/logs/yanet-pipeline-operator.log; do echo \"--- $file ---\"; tail -n 100 \"$file\"; done")
		return fmt.Errorf("wait for operator profile: %w\n%s\n%s", err, output, logs)
	}
	return nil
}

var operatorFiles = map[string]string{
	"/tmp/yanet/operators/configure-bird.sh": `#!/bin/bash
/tmp/yanet/operators/yanet-bird-adapter client \
  --server-config /tmp/yanet/config/operators/bird-adapter.yaml \
  --config route0 \
  --sockets /tmp/yanet/bird/master4.sock,/tmp/yanet/bird/master6.sock \
  --source-v4 127.0.0.1 \
  --source-v6 ::1 \
  --log-level debug
`,
	"/tmp/yanet/config/operators/route.yaml": `logging: {level: info}
server: {endpoint: "[::1]:50002"}
gateways: [{name: numa0, endpoint: "[::1]:8080"}]
register: {interval: 1s}
reconcile: {interval: 1s, initial_backoff: 100ms, max_backoff: 1s}
function: {name: "fn:route", chain: default, weight: 1, module: route0}
static:
  routes:
    - {prefix: 0.0.0.0/0, nexthop_addr: 203.0.113.1}
    - {prefix: "::/0", nexthop_addr: "fe80::1"}
  neighbours:
    - {next_hop: 203.0.113.1, link_addr: "52:54:00:6b:ff:a1", hardware_addr: "52:54:00:6b:ff:a5", device: "01:00.0"}
    - {next_hop: "fe80::1", link_addr: "52:54:00:6b:ff:a1", hardware_addr: "52:54:00:6b:ff:a5", device: "01:00.0"}
    - {next_hop: "2001:db8::1", link_addr: "52:54:00:6b:ff:a1", hardware_addr: "52:54:00:6b:ff:a5", device: "01:00.0"}
link_map: {kni0: "01:00.0"}
netlink_monitor: {disabled: true}
readiness: {expect_bird: true, rate_threshold: 50, stability_window: 1s, sample_interval: 1s, reconnect_grace: 1s}
`,
	"/tmp/yanet/config/operators/forward.yaml": `logging: {level: info}
server: {endpoint: "[::1]:50003"}
gateways: [{name: numa0, endpoint: "[::1]:8080"}]
register: {interval: 1s}
reconcile: {interval: 1s, initial_backoff: 100ms, max_backoff: 1s}
functions:
  - {name: "fn:forward", chain: default, weight: 1, module: forward0, rules_file: /tmp/yanet/config/operators/forward-rules.yaml}
`,
	"/tmp/yanet/config/operators/forward-rules.yaml": `rules:
  - {target: virtio_user_kni0, counter: to_kni, vlan_ranges: [{from: 0, to: 4095}], srcs: ["0.0.0.0/0", "::/0"], dsts: ["203.0.113.14/32", "fe80::/64"], mode: Out, devices: ["01:00.0"]}
  - {target: "01:00.0", counter: to_phy, vlan_ranges: [{from: 0, to: 4095}], srcs: ["0.0.0.0/0", "::/0"], dsts: ["0.0.0.0/0", "::/0"], mode: None, devices: ["01:00.0"]}
`,
	"/tmp/yanet/config/operators/decap.yaml": `logging: {level: info}
server: {endpoint: "[::1]:50004"}
gateways: [{name: numa0, endpoint: "[::1]:8080"}]
register: {interval: 1s}
reconcile: {interval: 1s, initial_backoff: 100ms, max_backoff: 1s}
functions:
  - {name: "fn:decap", chain: default, weight: 1, module: decap0, prefixes_file: /tmp/yanet/config/operators/decap-prefixes.yaml}
`,
	"/tmp/yanet/config/operators/decap-prefixes.yaml": "prefixes: []\n",
	"/tmp/yanet/config/operators/pipeline.yaml": `logging: {level: info}
server: {endpoint: "[::1]:50001"}
gateways: [{name: numa0, endpoint: "[::1]:8080"}]
register: {interval: 1s}
reconcile: {interval: 1s, initial_backoff: 100ms, max_backoff: 1s}
stages:
  - name: default
    pipelines:
      - {name: bootstrap, functions: ["fn:forward"]}
      - {name: test, functions: ["fn:forward", "fn:decap", "fn:lab", "fn:route"]}
      - {name: dummy, functions: []}
    devices:
      plain:
        - {name: "01:00.0", input: [{name: test, weight: 1}], output: [{name: dummy, weight: 1}]}
        - {name: virtio_user_kni0, input: [{name: bootstrap, weight: 1}], output: [{name: dummy, weight: 1}]}
      vlan: []
`,
	"/tmp/yanet/config/operators/bird-adapter.yaml": "logging: {level: info}\nlisten_addr: 127.0.0.1:50051\nroute_operator_endpoint: \"[::1]:50002\"\n",
	"/tmp/yanet/config/operators/bird.conf": `router id 127.0.0.1;
protocol device {}
protocol static static4 { ipv4; route 198.51.100.0/24 via 203.0.113.1; }
protocol static static6 { ipv6; route 2001:db8:100::/48 via 2001:db8::1; }
protocol export lab4 { table master4; socket "/tmp/yanet/bird/master4.sock"; }
protocol export lab6 { table master6; socket "/tmp/yanet/bird/master6.sock"; }
`,
}
