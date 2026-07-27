#pragma once

#include <stdbool.h>
#include <stdint.h>

struct packet;
typedef struct fwmap fwmap_t;

// Import sync direction enum
#include "sync.h"

/**
 * Check if a state exists for the given packet.
 * Builds the appropriate key based on packet IP version and performs lookup
 * in the matching map (fw4state for IPv4, fw6state for IPv6).
 *
 * Both maps are passed explicitly so that the key family and the map family
 * are decided by the same packet-type branch inside this function — a
 * key/map mismatch is impossible by construction.
 *
 * @param fw4state The IPv4 firewall state map (may be NULL)
 * @param fw6state The IPv6 firewall state map (may be NULL)
 * @param packet The packet to check state for
 * @param now Current time in nanoseconds
 * @param sync_required Output parameter indicating if sync is required
 * @return true if state was found, false otherwise
 */
bool
fwstate_check_state(
	fwmap_t *fw4state,
	fwmap_t *fw6state,
	struct packet *packet,
	uint64_t now,
	enum sync_packet_direction *sync_required
);
