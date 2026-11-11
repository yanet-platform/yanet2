#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "lib/counters/counters.h"

struct l3b_test_counter_witness {
	uint64_t matches;
	uint64_t failures;
	uint64_t registered_before;
};

int
l3b_test_counter_fault_begin(
	const char *service, const char *counter, uint64_t size
);

struct l3b_test_counter_witness
l3b_test_counter_fault_end(void);

struct l3b_test_allocation_witness {
	uint64_t matches;
	uint64_t registered_counters;
	uint64_t reserved_blocks;
	uint64_t reserved_bytes;
	int exhausted;
};

int
l3b_test_allocation_fault_begin(const char *service);

struct l3b_test_allocation_witness
l3b_test_allocation_fault_end(void);

struct l3b_test_counter_link {
	const char *device;
	const char *pipeline;
	const char *function;
	const char *chain;
	const char *module_type;
	const char *module;
};

enum { L3B_TEST_COUNTER_CAPACITY = 16 };

struct l3b_test_counter {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t offset;
	uint64_t values[2];
	int link;
};

struct l3b_test_counter_state {
	uintptr_t service;
	uintptr_t table;
	uint64_t table_references;
	uintptr_t object_storage;
	uintptr_t link_storage;
	uint64_t count;
	struct l3b_test_counter counters[L3B_TEST_COUNTER_CAPACITY];
};

int
l3b_test_counter_state_read(
	uintptr_t ectx,
	const char *service,
	const struct l3b_test_counter_link *link,
	struct l3b_test_counter_state *out
);

struct agent;
struct cp_module;

struct l3b_test_service_lookup {
	uintptr_t generation;
	uint64_t generation_number;
	uintptr_t object;
	uintptr_t counter_registry;
	uint64_t counter_id;
	// The lookup went through a generation a racing publication replaced
	// while its reader still pinned it; provenance fields stay unset.
	bool racing;
};

struct l3b_test_lookup_witness {
	uint64_t object_lookups;
	uint64_t counter_lookups;
	uint64_t unprotected_objects;
	uint64_t unprotected_counters;
	uint64_t provenance_errors;
	// Lookups that a concurrent publication legitimately diverted from
	// the published generation; protected by the reader's pin.
	uint64_t racing_objects;
	struct l3b_test_service_lookup lookups[L3B_TEST_COUNTER_CAPACITY];
};

int
l3b_test_lookup_begin(struct agent *agent);

struct l3b_test_lookup_witness
l3b_test_lookup_end(void);

int
l3b_test_module_counter_ids(
	struct cp_module *module, uint64_t *ids, uint32_t count
);
