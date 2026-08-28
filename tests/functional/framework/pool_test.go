package framework

import (
	"strings"
	"testing"
)

func TestConfigureTemplateSetsGuestPathsForSelectedSnapshot(t *testing.T) {
	pool := &VMPool{
		vms: []*poolEntry{{
			manager: &QEMUManager{},
			fw:      &TestFramework{},
		}},
	}
	templateOverlay := "template.qcow2"

	testCases := []struct {
		name             string
		snapshotName     string
		expectedPaths    GuestPaths
		expectedCommands []string
	}{
		{
			name:             "baseline",
			snapshotName:     "baseline",
			expectedPaths:    LocalGuestPaths(),
			expectedCommands: []string{"/tmp/yanet/forward.yaml", "/tmp/yanet/config/route0.yaml"},
		},
		{
			name:             "versioned baseline",
			snapshotName:     "baseline-custom-v3",
			expectedPaths:    LocalGuestPaths(),
			expectedCommands: []string{"/tmp/yanet/forward.yaml", "/tmp/yanet/config/route0.yaml"},
		},
		{
			name:             "booted fallback",
			snapshotName:     BootedSnapshotName,
			expectedPaths:    DefaultGuestPaths(),
			expectedCommands: []string{"/mnt/config/forward.yaml", "/mnt/config/route0.yaml"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool.configureTemplate(templateOverlay, testCase.snapshotName)

			entry := pool.vms[0]
			if entry.manager.TemplateOverlay != templateOverlay {
				t.Errorf("template overlay = %q, want %q", entry.manager.TemplateOverlay, templateOverlay)
			}
			if entry.manager.TemplateSnapshotName != testCase.snapshotName {
				t.Errorf("template snapshot = %q, want %q", entry.manager.TemplateSnapshotName, testCase.snapshotName)
			}
			if entry.fw.Paths != testCase.expectedPaths {
				t.Errorf("guest paths = %#v, want %#v", entry.fw.Paths, testCase.expectedPaths)
			}

			commands := strings.Join(entry.fw.CommonConfigCommands(), "\n")
			for _, expectedCommand := range testCase.expectedCommands {
				if !strings.Contains(commands, expectedCommand) {
					t.Errorf("common configuration commands do not contain %q", expectedCommand)
				}
			}
		})
	}
}

// Test_ResolveCLIPaths_GuestStorageMode verifies that snapshot-backed guests
// use copied CLIs while booted guests use the shared mount.
func Test_ResolveCLIPaths_GuestStorageMode(t *testing.T) {
	command := "/mnt/target/release/yanet-cli-route list"

	local := &TestFramework{Paths: LocalGuestPaths()}
	if got, want := local.resolveCLIPaths(command), "/tmp/yanet/cli/yanet-cli-route list"; got != want {
		t.Errorf("resolveCLIPaths() = %q, want %q", got, want)
	}

	mounted := &TestFramework{Paths: DefaultGuestPaths()}
	if got := mounted.resolveCLIPaths(command); got != command {
		t.Errorf("resolveCLIPaths() = %q, want %q", got, command)
	}
}
