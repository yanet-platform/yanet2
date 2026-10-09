//go:build ignore

// Writes the boot configuration of the Rust decap lab manifests: the lab's
// baseline dataplane YAML plus the plugin directory, and the baseline
// control-plane YAML with 4 MB agents.
//
// The Rust api manifest restarts the control plane with another build; the
// agents of the first process stay attached until superseded, so both sets
// must fit the instance's control-plane memory at once.
//
// Run from the repository root:
//
//	go run rust/sdk-poc-bindgen/lab/gen_config.go
package main

import (
	"os"
	"regexp"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func main() {
	for _, dir := range []string{"rust/sdk-poc-bindgen/lab/decap-rs/", "rust/sdk-poc-bindgen/lab/decap-rs-c-api/"} {
		write(dir)
	}
}

// write stores both boot configurations in the manifest directory dir.
func write(dir string) {
	dataplane := framework.DataplaneConfig(framework.DataplaneOptions{PluginDir: framework.LocalGuestPaths().PluginDir})
	if err := os.WriteFile(dir+"dataplane.yaml", []byte(dataplane), 0o644); err != nil {
		panic(err)
	}
	controlplane := regexp.MustCompile(`memory_requirements: \d+MB`).
		ReplaceAllString(framework.DefaultControlplaneConfig(), "memory_requirements: 4MB")
	if err := os.WriteFile(dir+"controlplane.yaml", []byte(controlplane), 0o644); err != nil {
		panic(err)
	}
}
