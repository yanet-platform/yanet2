package framework

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRestartYANETGuardsMissingConfig verifies that RestartYANET rejects a
// framework with no recorded dataplane configuration.
//
// The guard must return before touching anything that requires a live
// guest, so the call returns an error instead of panicking on a nil logger.
// It covers both a framework that never recorded anything and one that
// recorded a controlplane config but not a dataplane one, so the guard is
// pinned to the dataplane field rather than to either field being
// non-empty.
func TestRestartYANETGuardsMissingConfig(t *testing.T) {
	testCases := []struct {
		name string
		fw   *TestFramework
	}{
		{
			name: "nothing recorded",
			fw:   &TestFramework{},
		},
		{
			name: "controlplane config recorded but dataplane config still empty",
			fw:   &TestFramework{lastControlplaneConfig: "logging:\n  level: info\n"},
		},
	}

	for idx, testCase := range testCases {
		err := testCase.fw.RestartYANET()
		if err == nil {
			t.Fatalf("case %d (%s): RestartYANET() error = nil, want non-nil", idx, testCase.name)
		}

		if !strings.Contains(err.Error(), "no recorded configuration") {
			t.Errorf("case %d (%s): RestartYANET() error = %q, want it to describe the no-recorded-configuration case", idx, testCase.name, err.Error())
		}
	}
}

// TestAdoptRunningConfigRecordsConfig verifies that AdoptRunningConfig
// records both configuration YAML documents.
//
// Recording both is what lets a later RestartYANET call get past its
// no-recorded-configuration guard.
func TestAdoptRunningConfigRecordsConfig(t *testing.T) {
	dataplaneConfig := "dataplane:\n  storage: /dev/hugepages/yanet\n"
	controlplaneConfig := "logging:\n  level: info\n"

	fw := &TestFramework{}

	fw.AdoptRunningConfig(dataplaneConfig, controlplaneConfig)

	if fw.lastDataplaneConfig != dataplaneConfig {
		t.Errorf("lastDataplaneConfig = %q, want %q", fw.lastDataplaneConfig, dataplaneConfig)
	}
	if fw.lastControlplaneConfig != controlplaneConfig {
		t.Errorf("lastControlplaneConfig = %q, want %q", fw.lastControlplaneConfig, controlplaneConfig)
	}
}

// TestPromptAwareSplit verifies the serial scanner split function emits
// newline-terminated lines normally, but also emits the unterminated shell
// prompt so readiness detection works without writing into the console
// during boot.
func TestPromptAwareSplit(t *testing.T) {
	prompt := "root@yanet-vm:~#"
	scan := func(input string) func(data []byte, atEOF bool) (int, []byte, error) {
		return func(data []byte, atEOF bool) (int, []byte, error) {
			return promptAwareSplit(data, atEOF)
		}
	}
	type token struct {
		input  string
		atEOF  bool
		expect string
	}
	testCases := []struct {
		name   string
		tokens []token
	}{
		{
			name: "normal lines",
			tokens: []token{
				{input: "Linux version 6.8.0\nmore stuff\n", atEOF: true, expect: "Linux version 6.8.0"},
				{input: "more stuff\n", atEOF: false, expect: "more stuff"},
			},
		},
		{
			name: "fragmented prompt at end without newline",
			tokens: []token{
				{input: "some boot log\nroot@yanet-vm:~#", atEOF: true, expect: "some boot log\nroot@yanet-vm:~#"},
			},
		},
		{
			name: "incomplete non-prompt data waits for more",
			tokens: []token{
				{input: "partial line without newline", atEOF: false, expect: ""},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, tok := range testCase.tokens {
				data := []byte(tok.input)
				advance, tokenBytes, err := scan("")(data, tok.atEOF)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tok.expect == "" {
					if advance != 0 || tokenBytes != nil {
						t.Fatalf("expected no token, got advance=%d token=%q", advance, string(tokenBytes))
					}
					continue
				}
				if string(tokenBytes) != tok.expect {
					t.Fatalf("token = %q, want %q (advance=%d)", string(tokenBytes), tok.expect, advance)
				}
			}
		})
	}

	// Verify the prompt is detected even mid-buffer.
	data := []byte("boot line\n" + prompt)
	advance, tokenBytes, err := promptAwareSplit(data, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(tokenBytes), prompt) {
		t.Fatalf("expected token to contain prompt, got %q (advance=%d)", string(tokenBytes), advance)
	}
}

// TestClassifyProcessExit verifies the three exit paths: clean exit,
// non-zero exit code, and signal death.
func TestClassifyProcessExit(t *testing.T) {
	// Clean exit (code 0).
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	msg := classifyProcessExit(cmd, nil)
	if !strings.Contains(msg, "exit code 0") {
		t.Fatalf("clean exit: got %q", msg)
	}

	// Non-zero exit code.
	cmd = exec.Command("false")
	err := cmd.Run()
	msg = classifyProcessExit(cmd, err)
	if !strings.Contains(msg, "exit code 1") {
		t.Fatalf("non-zero exit: got %q", msg)
	}

	// Signal death.
	cmd = exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	waitErr := cmd.Process.Kill()
	if waitErr != nil {
		t.Fatalf("kill: %v", waitErr)
	}
	err = cmd.Wait()
	msg = classifyProcessExit(cmd, err)
	if !strings.Contains(msg, "signal") {
		t.Fatalf("signal death: got %q", msg)
	}
}
