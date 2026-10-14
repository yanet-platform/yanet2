#pragma once

#include <stdint.h>

#include "lib/controlplane/config/cp_module.h"

#include "mode.h"

struct rte_bpf;

// Sentinel for "no ring linked". A link index past the configured links
// resolves to nothing, so a config with no link at this slot resolves to
// no ring and the handler captures nothing.
#define PDUMP_RING_LINK_NONE UINT64_MAX

struct pdump_module_config {
	struct cp_module cp_module;

	char *filter;
	struct rte_bpf *ebpf_program;
	enum pdump_mode mode;
	uint32_t snaplen;

	// Object link index of the ring this config captures into.
	//
	// The control plane declares the link by ring name; the dataplane
	// resolves it into a per-worker ring context whenever it builds the
	// execution contexts. PDUMP_RING_LINK_NONE marks an absent link.
	uint64_t ring_link_idx;
};
