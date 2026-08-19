#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"

struct dp_config;
struct dp_worker;

enum {
	WORKER_RX_BURST_SIZE = 32,
};

// A worker counter as it is registered.
struct worker_counter_spec {
	const char *name;
	uint64_t size;
};

// A worker field that receives the address of a single counter value.
//
// The offset locates the pointer field inside the worker struct, the counter
// is named, and the value index picks the slot inside it whose address that
// field receives.
struct worker_counter_field {
	size_t offset;
	const char *counter;
	uint64_t value_idx;
};

// The standard worker counters.
extern const struct worker_counter_spec worker_counter_specs[];
extern const size_t worker_counter_spec_count;

// The worker fields bound to those counters.
extern const struct worker_counter_field worker_counter_fields[];
extern const size_t worker_counter_field_count;

// Register standard worker counters in dp_config->worker_counters. Caller must
// initialise the registry via counter_registry_init before calling.
//
// Returns 0 on success, -1 if any counter fails to register.
int
worker_counters_register(struct dp_config *dp_config);

// Point a worker's counter fields at their storage slots.
//
// Every counter is resolved by name and its addressed value must fall inside
// the size it was registered with. The worker's counter storage must already
// be spawned and wired into the config. On failure the worker's fields are left
// unchanged.
//
// Returns 0 on success, -1 if any field fails to resolve.
int
worker_counters_bind(struct dp_worker *dp_worker, struct dp_config *dp_config);
