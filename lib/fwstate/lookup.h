#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "fwtable.h"

struct packet;

// Import sync direction enum
#include "sync.h"

/**
 * Check if a state exists for the given packet in an fwtable.
 * Builds the appropriate key based on packet IP version and performs
 * lookup across all layers of the table.
 *
 * The table serves a single family, so the caller picks it per packet
 * family — a v4 packet is checked against the v4 map object's table and
 * a v6 packet against the v6 object's.
 *
 * @param table The firewall state table (may be NULL)
 * @param packet The packet to check state for
 * @param now Current time in nanoseconds
 * @param sync_required Output parameter indicating if sync is required
 * @return true if state was found, false otherwise
 */
bool
fwstate_check_state_table(
	fwtable_t *table,
	struct packet *packet,
	uint64_t now,
	enum sync_packet_direction *sync_required
);
