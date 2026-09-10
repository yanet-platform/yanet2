#pragma once

#include "config.h"
#include <stdbool.h>
#include <stdint.h>

// Forward declarations
struct dp_worker;
struct packet;

enum sync_packet_direction {
	// NOLINTBEGIN(readability-identifier-naming)
	SYNC_NONE,
	SYNC_INGRESS,
	SYNC_EGRESS,
	// NOLINTEND(readability-identifier-naming)
};

static inline bool
fwstate_sync_multicast_enabled(const struct fwstate_sync_config *config) {
	return config->port_multicast != 0;
}

static inline bool
fwstate_sync_unicast_enabled(const struct fwstate_sync_config *config) {
	return config->port_unicast != 0;
}

/**
 * Craft a state synchronization packet from the given packet.
 *
 * @param packet The original packet to extract 5-tuple from
 * @param direction The direction of the sync packet (INGRESS or EGRESS)
 * @param sync_pkt Pre-allocated packet to fill with sync data
 * @return 0 on success, or -1 on failure
 */
int
fwstate_craft_state_sync_packet(
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct packet *sync_pkt
);

// Outer wire addressing is assigned only after local state accepts the event.
void
fwstate_sync_set_destination(
	struct packet *packet,
	const struct ether_addr *dst_ether,
	const uint8_t dst_addr[16],
	uint16_t dst_port
);
