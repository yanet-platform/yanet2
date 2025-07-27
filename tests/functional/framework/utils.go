package framework

import (
	"fmt"
	"os"
	"path/filepath"
)

// findProjectRoot finds the project root directory by looking for meson.build and build directory
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
