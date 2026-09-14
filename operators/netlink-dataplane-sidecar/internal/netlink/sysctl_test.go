package netlink_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
)

// Test_ProcSysctl_PinsDescriptorBeforeValidation verifies that replacing the
// path during validation cannot redirect a write to the replacement file.
func Test_ProcSysctl_PinsDescriptorBeforeValidation(t *testing.T) {
	root := t.TempDir()
	interfaceDirectory := filepath.Join(root, "kni0")
	require.NoError(t, os.Mkdir(interfaceDirectory, 0o755))
	path := filepath.Join(interfaceDirectory, "accept_ra")
	openedPath := path + ".opened"
	require.NoError(t, os.WriteFile(path, []byte("0"), 0o644))
	sysctl := &netreconcile.ProcSysctl{Root: root}

	err := sysctl.SetIPv6(t.Context(), "kni0", "accept_ra", "2", func() error {
		require.NoError(t, os.Rename(path, openedPath))
		return os.WriteFile(path, []byte("1"), 0o644)
	})

	require.NoError(t, err)
	replacement, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "1", string(replacement))
	opened, err := os.ReadFile(openedPath)
	require.NoError(t, err)
	require.Equal(t, "2", string(opened))
}

// Test_ProcSysctl_RejectsFailedValidation verifies that a link validation error
// is returned without changing the previously opened sysctl file.
func Test_ProcSysctl_RejectsFailedValidation(t *testing.T) {
	root := t.TempDir()
	interfaceDirectory := filepath.Join(root, "kni0")
	require.NoError(t, os.Mkdir(interfaceDirectory, 0o755))
	path := filepath.Join(interfaceDirectory, "addr_gen_mode")
	require.NoError(t, os.WriteFile(path, []byte("0"), 0o644))
	sysctl := &netreconcile.ProcSysctl{Root: root}
	validationErr := errors.New("link replaced")

	err := sysctl.SetIPv6(t.Context(), "kni0", "addr_gen_mode", "1", func() error {
		return validationErr
	})

	require.ErrorIs(t, err, validationErr)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "0", string(contents))
}
