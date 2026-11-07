#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/rx_service.h"

#include "assert.h"

#include <stdint.h>

// A queue serving scripted burst sizes: each poll returns the next entry,
// and a queue stays in its final state once the script runs out.
struct scripted_queue {
	const uint16_t *script;
	uint16_t length;
	uint16_t position;
};

static uint16_t
scripted_queue_burst(struct scripted_queue *queue, uint16_t read_size) {
	uint16_t burst_size = queue->script[queue->position];
	if (queue->position + 1 < queue->length) {
		++queue->position;
	}
	return burst_size > read_size ? read_size : burst_size;
}

// One round of an assignment's rx service over the production decision.
//
// Polls up to weight bursts, stopping at the first that does not fill
// the read window; returns the polls the round took.
static uint16_t
serve_round(struct scripted_queue *queue, uint16_t weight) {
	uint16_t polls = 0;
	for (uint16_t burst = 0; burst < weight; ++burst) {
		uint16_t read =
			scripted_queue_burst(queue, WORKER_RX_BURST_SIZE);
		++polls;
		if (!worker_rx_service_continues(read, WORKER_RX_BURST_SIZE)) {
			break;
		}
	}
	return polls;
}

// The configured service bound: the parser range and the staging
// allocation both size against it.
static void
test_weight_bound(void) {
	assert(WORKER_RX_WEIGHT_MAX == 16);
}

// A full queue costs the whole weight every round; a drained queue costs
// a single poll, whatever the weight.
static void
test_per_round_polls(void) {
	const uint16_t full[] = {32};
	struct scripted_queue queue = {
		.script = full,
		.length = 1,
	};
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 1) == 1);

	queue.position = 0;
	assert(serve_round(&queue, 16) == 16);
	assert(serve_round(&queue, 16) == 16);

	const uint16_t empty[] = {0};
	queue.script = empty;
	queue.length = 1;
	queue.position = 0;
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 16) == 1);
}

// A short burst inside the budget ends the assignment's service for the
// round; the next round starts fresh at one poll on the drained queue.
static void
test_early_stop(void) {
	const uint16_t drain_after_two[] = {32, 32, 5, 0};
	struct scripted_queue queue = {
		.script = drain_after_two,
		.length = 4,
	};
	assert(serve_round(&queue, 4) == 3);
	assert(serve_round(&queue, 4) == 1);
	assert(serve_round(&queue, 4) == 1);
}

// Weight 1 is the degenerate single-assignment service: one poll per
// round, and a full queue never earns a second one.
static void
test_weight_one_degenerate(void) {
	const uint16_t full_then_short[] = {32, 3};
	struct scripted_queue queue = {
		.script = full_then_short,
		.length = 2,
	};
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 1) == 1);
}

// Assignments are served in configuration order, every round, each on
// its own budget.
static void
test_cyclic_order(void) {
	const uint16_t full[] = {32};
	struct scripted_queue queues[2] = {
		{.script = full, .length = 1},
		{.script = full, .length = 1},
	};
	const uint16_t weights[2] = {2, 1};

	for (uint32_t round = 0; round < 3; ++round) {
		uint16_t polls[2];
		for (uint32_t asgn_idx = 0; asgn_idx < 2; ++asgn_idx) {
			polls[asgn_idx] = serve_round(
				&queues[asgn_idx], weights[asgn_idx]
			);
		}
		assert(polls[0] == 2);
		assert(polls[1] == 1);
	}
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	test_weight_bound();
	test_per_round_polls();
	test_early_stop();
	test_weight_one_degenerate();
	test_cyclic_order();

	return 0;
}
