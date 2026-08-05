package framework

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
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

func TestReceiveAllPacketsUnfilteredReportsPartialFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	clientSocket := &SocketClient{inner: &socketClientInner{conn: client}}
	go func() {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], 4)
		_, _ = server.Write(length[:])
		_, _ = server.Write([]byte{1})
	}()

	_, err := clientSocket.ReceiveAllPacketsUnfiltered(20*time.Millisecond, "")

	if err == nil || !strings.Contains(err.Error(), "packet data") {
		t.Fatalf("error = %v, want partial packet error", err)
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
