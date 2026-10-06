#pragma once

#include "common/lpm.h"
#include "lib/controlplane/config/cp_module.h"

// Config of the Rust-implemented reference blackhole: packets whose
// destination matches a listed prefix are dropped and counted, the rest
// pass through.
//
// The Rust dataplane crate mirrors this struct byte for byte
// (modules/rblackhole/rust/src/lib.rs); keep both sides in one change.
struct rblackhole_module_config {
	struct cp_module cp_module;

	struct lpm prefixes4;
	struct lpm prefixes6;

	// Module-device link index of the device passed packets are routed
	// to; zero names the "any" fallback device cp_module_init links
	// first, so an unset config fails the routing and drops.
	uint64_t pass_device_idx;
	uint64_t dropped_counter_id;
};
