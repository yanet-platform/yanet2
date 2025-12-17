#pragma once

#include <stdbool.h>
#include <stdint.h>

struct packet;
typedef struct fwmap fwmap_t;

// Import sync direction enum
#include "sync.h"

/**
 * Check if a state exists for the given packet.
 * Builds the appropriate key based on packet IP version and performs lookup.
 *
 * @param fwstate The firewall state map
 * @param packet The packet to check state for
 * @param now Current time in nanoseconds
 * @param sync_required Output parameter indicating if sync is required
 * @return true if state was found, false otherwise
 */
bool
fwstate_check_state(
	fwmap_t *fwstate,
	struct packet *packet,
	uint64_t now,
	enum sync_packet_direction *sync_required
);
