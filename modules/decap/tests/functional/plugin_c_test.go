//go:build !yanet_rust_cp

package decap_test

// decapPluginDir is empty: the harness runs the built-in C decap module,
// which reads the configurations the default C api builds.
const decapPluginDir = ""
