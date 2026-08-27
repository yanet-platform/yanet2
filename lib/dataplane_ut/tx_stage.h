#pragma once

#include <stddef.h>
#include <stdint.h>

struct dataplane_ut_tx_stage;
struct packet;

// Construct a fixture owning a connection table and a mock mempool.
//
// Every device is given the same number of pipes; a device with none stands
// for one this worker cannot reach. Returns NULL on allocation failure.
struct dataplane_ut_tx_stage *
dataplane_ut_tx_stage_new(uint32_t device_count, uint32_t pipes_per_device);

// Tear down a fixture previously returned by dataplane_ut_tx_stage_new.
// NULL-safe.
void
dataplane_ut_tx_stage_free(struct dataplane_ut_tx_stage *fixture);

// Take a packet from the mock pool, addressed to a device and carrying a
// flow hash. Returns NULL on exhaustion.
struct packet *
dataplane_ut_tx_stage_packet(
	struct dataplane_ut_tx_stage *fixture,
	uint16_t tx_device_id,
	uint32_t hash
);

// Offer one packet to the staging path.
void
dataplane_ut_tx_stage_offer(
	struct dataplane_ut_tx_stage *fixture, struct packet *packet
);

// Hand every pipe's held packets to their consumers.
void
dataplane_ut_tx_stage_flush(struct dataplane_ut_tx_stage *fixture);

// How many packets a given pipe is currently holding.
//
// The coordinates must be within the table the fixture was built with.
uint32_t
dataplane_ut_tx_stage_held(
	struct dataplane_ut_tx_stage *fixture, uint32_t device, uint32_t pipe
);

// How many packets a given pipe has placed with its consumer.
uint64_t
dataplane_ut_tx_stage_placed(
	struct dataplane_ut_tx_stage *fixture, uint32_t device, uint32_t pipe
);

// Counters the staging path maintains.
uint64_t
dataplane_ut_tx_stage_tx_count(struct dataplane_ut_tx_stage *fixture);
uint64_t
dataplane_ut_tx_stage_tx_drops(struct dataplane_ut_tx_stage *fixture);

// The failure list, in the order packets joined it.
size_t
dataplane_ut_tx_stage_failed_count(struct dataplane_ut_tx_stage *fixture);
struct packet *
dataplane_ut_tx_stage_failed_at(
	struct dataplane_ut_tx_stage *fixture, size_t idx
);
