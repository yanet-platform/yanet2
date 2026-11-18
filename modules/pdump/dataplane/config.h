#pragma once

#include <stdint.h>

#include "lib/controlplane/config/cp_module.h"

#include "limit.h"
#include "mode.h"

struct ebpf_insn;

// Sentinel for "no ring linked". A link index past the configured links
// resolves to nothing, so a config with no link at this slot resolves to
// no ring and the handler captures nothing.
#define PDUMP_RING_LINK_NONE UINT64_MAX

struct pdump_module_config {
	struct cp_module cp_module;

	char *filter;
	// The compiled filter: eBPF instructions in DPDK's encoding.
	//
	// The dataplane wraps them in its own interpreter descriptor per
	// execution context, so the configuration holds no DPDK-private
	// layout. NULL with a zero count while no filter is set.
	struct ebpf_insn *filter_ins;
	uint32_t filter_ins_count;
	enum pdump_mode mode;
	uint32_t snaplen;
	struct token_bucket *rate_bucket;
	void *rate_bucket_raw;
	// Counts refused matches and packets skipped without filter evaluation.
	uint64_t rate_skipped_counter_id;

	// Object link index of the ring this config captures into.
	//
	// The control plane declares the link by ring name; the dataplane
	// resolves it into a per-worker ring context whenever it builds the
	// execution contexts. PDUMP_RING_LINK_NONE marks an absent link.
	uint64_t ring_link_idx;
};
