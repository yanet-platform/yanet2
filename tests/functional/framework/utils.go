package framework

import (
	"fmt"
	"os"
	"path/filepath"
)

// findProjectRoot locates the YANET project root directory by traversing up the
// directory tree from the current working directory and searching for project
// markers. This function is essential for establishing proper filesystem paths
// for build artifacts and project resources.
//
// The detection algorithm searches for both:
//   - meson.build file (indicating a Meson build system project)
//   - build/ directory (containing compiled artifacts and build outputs)
//
// The method starts from the current working directory and walks up the directory
// hierarchy until it finds a directory containing both required markers, or
// reaches the filesystem root.
//
// This approach ensures that tests can be executed from any subdirectory within
// the project while still correctly locating build artifacts and project resources.
//
// Returns:
//   - string: Absolute path to the project root directory
//   - error: An error if the current directory cannot be determined or project root is not found
//
// Example:
//
//	projectRoot, err := findProjectRoot()
//	if err != nil {
//	    log.Fatalf("Cannot locate project root: %v", err)
//	}
//	buildDir := filepath.Join(projectRoot, "build")
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree looking for meson.build and build directory
	for {
		mesonFile := filepath.Join(dir, "meson.build")
		buildDir := filepath.Join(dir, "build")

		// Check if both meson.build and build directory exist
		if _, err := os.Stat(mesonFile); err == nil {
			if _, err := os.Stat(buildDir); err == nil {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("project root not found (no meson.build with build directory)")
}

// ShouldPreserveArtifacts checks if test artifacts should be preserved for debugging.
// Returns true if the YANET_PRESERVE_ARTIFACTS environment variable is set.
//
// Example:
//
//	if ShouldPreserveArtifacts() {
//	    log.Info("Preserving test artifacts for debugging")
//	    // Skip cleanup
//	}
func ShouldPreserveArtifacts() bool {
	_, ok := os.LookupEnv("YANET_PRESERVE_ARTIFACTS")
	return ShouldKeepVMAlive() || ok
}

// IsDebugEnabled checks if debug mode is enabled for tests.
// Returns true if the YANET_TEST_DEBUG environment variable is set.
//
// Example:
//
//	if IsDebugEnabled() {
//	    log.Info("Debug mode enabled")
//	    // Enable verbose logging
//	}
func IsDebugEnabled() bool {
	_, ok := os.LookupEnv("YANET_TEST_DEBUG")
	return ShouldKeepVMAlive() || ok
}

// ShouldKeepVMAlive checks if the VM should be kept running after tests.
// Returns true if the YANET_KEEP_VM_ALIVE environment variable is set.
// When enabled, the QEMU process will not be killed, allowing manual
// connection to the serial console for debugging.
//
// Example:
//
//	if ShouldKeepVMAlive() {
//	    log.Info("VM will remain running for manual debugging")
//	    // Skip QEMU process termination
//	}
func ShouldKeepVMAlive() bool {
	_, ok := os.LookupEnv("YANET_KEEP_VM_ALIVE")
	return ok
}
