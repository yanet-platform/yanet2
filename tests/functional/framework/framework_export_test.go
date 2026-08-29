package framework

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_ExportOverlay_PausesBeforeCopy verifies that a writable overlay is
// copied only after the owning VM has acknowledged the pause command.
func Test_ExportOverlay_PausesBeforeCopy(t *testing.T) {
	var calls []string
	err := exportOverlay(
		func(command string) (string, error) {
			calls = append(calls, command)
			return "", nil
		},
		func(source, destination string) error {
			calls = append(calls, source+"->"+destination)
			return nil
		},
		"source.qcow2",
		"destination.qcow2",
	)

	require.NoError(t, err)
	require.Equal(t, []string{"stop", "source.qcow2->destination.qcow2"}, calls)
}

// Test_ExportOverlay_PauseFailureSkipsCopy verifies that an unpaused VM can
// never race a host-side copy of its writable overlay.
func Test_ExportOverlay_PauseFailureSkipsCopy(t *testing.T) {
	copied := false
	pauseError := errors.New("monitor unavailable")
	err := exportOverlay(
		func(string) (string, error) { return "", pauseError },
		func(string, string) error {
			copied = true
			return nil
		},
		"source.qcow2",
		"destination.qcow2",
	)

	require.ErrorIs(t, err, pauseError)
	require.False(t, copied)
}
