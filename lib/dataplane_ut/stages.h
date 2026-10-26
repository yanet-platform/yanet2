#pragma once

#include "dataplane_ut.h"

// Prepare a real worker round without polling its device entries.
//
// Each stage drains its packets before returning. Starting another round
// advances the worker iteration and invalidates the previous stage sequence.
int
dataplane_ut_begin_round(struct dataplane_ut *ut, size_t worker_idx);

struct dataplane_ut_stage {
	size_t worker_idx;
	uint16_t device_id;
	bool output;
	uint16_t packet_flags;
};

// Execute one published device entry and all redirects it schedules.
//
// The round must have been opened against the current generation and must
// not have been advanced by ordinary round execution. Input is always
// drained, including on validation failure, and the result owns all packets.
int
dataplane_ut_run_stage(
	struct dataplane_ut *ut,
	const struct dataplane_ut_stage *stage,
	struct packet_list *input,
	struct dataplane_ut_round_result *result
);

// Bound the additional live pool allocations until the budget is reset.
//
// Minus one restores the normal pool capacity. Zero forces an actual
// allocation failure; one permits a batch but refuses its simultaneous clone.
int
dataplane_ut_set_allocation_budget(struct dataplane_ut *ut, int64_t budget);

// Append one owned raw input, or leave the list unchanged on failure.
// Payloads must fit a single mbuf including its descriptor headroom.
int
dataplane_ut_stage_input_append(
	struct packet_list *input, const void *data, size_t size
);
