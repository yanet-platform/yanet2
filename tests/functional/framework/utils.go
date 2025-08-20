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
