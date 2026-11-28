//go:build sdk_rust_fixtures

// Package dataplane holds the tests that run the SDK fixture modules as
// plugins in the dataplane harness, over configurations the native
// control plane builds and publishes, and what they take from C: the
// plugin directory and the room a packet has to grow into.
package dataplane

/*
#include <rte_build_config.h>

#include "lib/dataplane/packet/packet.h"
#include "yanet_sdk_fixture_paths.h"
*/
import "C"

// PluginDir holds the fixture plugins of the configured Meson build.
const PluginDir = C.YANET_SDK_FIXTURE_PLUGIN_DIR

// PrependRoom is how many bytes a packet made from the worker pool can have
// prepended to its frame: the headroom of its buffer less the packet
// descriptor, which shares the buffer in front of the frame.
const PrependRoom = int(C.RTE_PKTMBUF_HEADROOM - C.sizeof_struct_packet)
