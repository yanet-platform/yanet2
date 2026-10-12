#pragma once

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
