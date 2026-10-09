#pragma once

// Out-of-line entry points for dataplane helpers that C defines inline.
//
// Rust cannot call static inline functions or DPDK macros, so each helper
// gets a callable wrapper here; wrappers that resize the frame also refresh
// the cached length. The header stays DPDK-free: bindgen reads it without
// the DPDK include tree.

#include <stdint.h>

struct packet;
struct packet_front;

struct packet *
yanet_dp_shim_packet_front_pop_input(struct packet_front *packet_front);

void
yanet_dp_shim_packet_front_output(
	struct packet_front *packet_front, struct packet *packet
);

void
yanet_dp_shim_packet_front_drop(
	struct packet_front *packet_front, struct packet *packet
);

// Start of the first segment's data; its length is stored to data_len.
uint8_t *
yanet_dp_shim_packet_data(struct packet *packet, uint16_t *data_len);

// Length of the whole packet across all segments.
uint32_t
yanet_dp_shim_packet_len(struct packet *packet);

// Grow the frame at the front by len bytes inside the headroom.
//
// Returns the new data start, or NULL when the headroom left above the
// packet descriptor is too small; the cached length is refreshed.
uint8_t *
yanet_dp_shim_packet_prepend(struct packet *packet, uint16_t len);

// Remove len bytes from the front of the first segment.
//
// Returns 0 on success, -1 when the first segment is shorter than len;
// the cached length is refreshed.
int
yanet_dp_shim_packet_adj(struct packet *packet, uint16_t len);

// Re-run the packet parser over the current frame.
//
// Returns 0 on success, -1 when the frame does not parse.
int
yanet_dp_shim_packet_parse(struct packet *packet);
