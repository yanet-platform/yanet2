package framework

import (
	"strings"
	"testing"
)

func TestBaselinePoolUsesLocalGuestPaths(t *testing.T) {
	paths := guestPathsForTemplate("baseline")
	if !paths.LocalMode {
		t.Fatal("baseline pool must use guest-local paths")
	}
	if guestPathsForTemplate(BootedSnapshotName).LocalMode {
		t.Fatal("booted pool must use 9P paths before local storage is prepared")
	}

	fw := &TestFramework{Paths: paths}
	commands := strings.Join(fw.CommonConfigCommands(), "\n")
	for _, want := range []string{
		"/tmp/yanet/forward.yaml",
		"/tmp/yanet/config/route0.yaml",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("common configuration commands do not contain %q", want)
		}
	}
}
