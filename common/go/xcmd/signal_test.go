package xcmd_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcmd"
)

// The child process that signals itself runs only when both the environment
// marker and the positional argument are present, so an inherited environment
// alone never makes an ordinary test run send SIGINT to itself.
const (
	releaseChildEnvironment = "XCMD_WAIT_INTERRUPTED_RELEASE_CHILD"
	releaseChildArgument    = "xcmd-wait-interrupted-release-child"
)

// Test_WaitInterrupted_ReleasesSignalsAfterReturn verifies that once the wait
// returns on a canceled context, a later SIGINT is no longer diverted to it
// and kills the process with the default action.
func Test_WaitInterrupted_ReleasesSignalsAfterReturn(t *testing.T) {
	if os.Getenv(releaseChildEnvironment) == "1" && slices.Contains(os.Args, releaseChildArgument) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if !errors.Is(xcmd.WaitInterrupted(ctx), context.Canceled) {
			os.Exit(3)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}

	// A handler installed here makes the child start with the default SIGINT
	// action even when this process inherited SIGINT as ignored.
	normalize := make(chan os.Signal, 1)
	signal.Notify(normalize, syscall.SIGINT)
	defer signal.Stop(normalize)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^Test_WaitInterrupted_ReleasesSignalsAfterReturn$",
		"--",
		releaseChildArgument,
	)
	command.Env = append(os.Environ(), releaseChildEnvironment+"=1")
	err := command.Run()

	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	status := exitError.Sys().(syscall.WaitStatus)
	require.True(t, status.Signaled(), "child exited with %v instead of being killed", exitError)
	require.Equal(t, syscall.SIGINT, status.Signal())
}
