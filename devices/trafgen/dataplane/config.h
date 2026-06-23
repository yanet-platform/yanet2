#pragma once

#include <stdint.h>

#include "controlplane/config/zone.h"

// Per-worker generation state.
//
// Each worker advances its own cursor and token bucket independently, so the
// slots live in a per-worker array indexed by dp_worker->idx with no
// cross-worker contention.
struct trafgen_worker_state {
	// Index of the next frame to emit; wraps modulo frame_count.
	uint64_t cursor;

	// Token bucket accumulator in packet-nanosecond units.
	//
	// Each tick adds elapsed_ns * rate_pps; one packet is emitted per
	// 1e9 * worker_count credit, which spreads rate_pps evenly across
	// all workers.
	uint64_t credit;

	// Worker time of the last token bucket refill, in nanoseconds.
	uint64_t last_time;
};

// Shared-memory configuration of the trafgen device.
//
// Frames extracted from the source pcap are stored as a single concatenated
// blob plus per-frame offset and length arrays. The dataplane replays them in
// order, cycling like a ring buffer, pacing emission to the target rate. The
// device embeds a cp_device, so the generated traffic is demultiplexed into the
// configured input pipelines exactly like a physical RX device.
struct cp_device_trafgen {
	struct cp_device cp_device;

	// Target aggregate packet rate in packets per second; 0 stops emission.
	uint64_t rate_pps;

	// Number of frames loaded from the source pcap.
	uint32_t frame_count;
	uint32_t pad;

	// Total byte length of the concatenated frames blob.
	uint64_t frames_size;

	// Concatenated raw L2 frame bytes (relative pointer).
	uint8_t *frames;
	// Per-frame byte offset into frames (relative pointer, frame_count
	// items).
	uint32_t *frame_offsets;
	// Per-frame byte length (relative pointer, frame_count items).
	uint32_t *frame_lengths;

	// Per-worker generation cursor (relative pointer, worker_count items).
	struct trafgen_worker_state *worker_states;
	uint64_t worker_count;
};
