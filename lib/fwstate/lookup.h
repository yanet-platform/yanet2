#pragma once

#include <stdbool.h>
#include <stdint.h>

struct packet;

#include "sync.h"

/**
 * Check if a state exists for the given packet.
 * Builds the appropriate key based on packet IP version and performs
 * lookup across all layers of the fwtable.
 *
 * @param table The firewall state table
 * @param packet The packet to check state for
 * @param now Current time in nanoseconds
 * @param sync_required Output parameter indicating if sync is required
 * @return true if state was found, false otherwise
 */
bool
fwstate_check_state(
	fwtable_t *table,
	struct packet *packet,
	uint64_t now,
	enum sync_packet_direction *sync_required
);
