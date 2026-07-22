#include <assert.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>

#include "common/data_pipe.h"

#define PIPE_SIZE_LOG2 8
#define NUM_ITERATIONS 100000

struct data_pipe shared_pipe;

static size_t
push_handler(void **items, size_t count, void *ctx) {
	uintptr_t *val = (uintptr_t *)ctx;
	if (count > 0) {
		items[0] = (void *)(*val);
		return 1;
	}
	return 0;
}

static size_t
pop_handler(void **items, size_t count, void *ctx) {
	uintptr_t *expected = (uintptr_t *)ctx;
	if (count > 0) {
		uintptr_t val = (uintptr_t)items[0];
		if (val != *expected) {
			fprintf(stderr,
				"DATA CORRUPTION: expected %lu, got %lu\n",
				(unsigned long)*expected,
				(unsigned long)val);
			return 0; // Don't process corrupted data.
		}
		*expected += 1;
		return 1;
	}
	return 0;
}

static size_t
free_handler(void **items, size_t count, void *ctx) {
	(void)items;
	(void)ctx;
	if (count > 0) {
		return 1;
	}
	return 0;
}

#define PARTIAL_PIPE_SIZE_LOG2 6
#define PARTIAL_NUM_ITERATIONS 200000
#define PARTIAL_PUSH_BURST 8

struct data_pipe partial_pipe;

// Producer callback: offers up to PARTIAL_PUSH_BURST sequential values in
// a single push, mirroring a worker that hands a whole tx_burst-sized
// batch to a pipe rather than one packet at a time.
static size_t
partial_push_handler(void **items, size_t count, void *ctx) {
	uintptr_t *next = (uintptr_t *)ctx;
	size_t remaining = PARTIAL_NUM_ITERATIONS - (*next - 1);

	size_t offer = count;
	if (offer > PARTIAL_PUSH_BURST) {
		offer = PARTIAL_PUSH_BURST;
	}
	if (offer > remaining) {
		offer = remaining;
	}

	for (size_t idx = 0; idx < offer; idx++) {
		items[idx] = (void *)(*next + idx);
	}
	*next += offer;

	return offer;
}

// Consumer callback: deliberately accepts only half of what is offered
// (rounded down, at least one), leaving a tail in the pipe for the next
// pop call. This mirrors retry semantics where a rejected mbuf tail stays
// in the pipe rather than being freed, so the r_pos protocol must stay
// correct across many consecutive rounds where pop returns less than
// count.
static size_t
partial_pop_handler(void **items, size_t count, void *ctx) {
	uintptr_t *expected = (uintptr_t *)ctx;

	size_t accept = count / 2;
	if (accept == 0) {
		accept = 1;
	}

	for (size_t idx = 0; idx < accept; idx++) {
		uintptr_t val = (uintptr_t)items[idx];
		if (val != *expected) {
			fprintf(stderr,
				"PARTIAL CORRUPTION: expected %lu, got %lu\n",
				(unsigned long)*expected,
				(unsigned long)val);
			return 0;
		}
		*expected += 1;
	}

	return accept;
}

static void *
partial_producer_thread(void *arg) {
	(void)arg;
	uintptr_t next = 1;

	while (next <= PARTIAL_NUM_ITERATIONS) {
		while (data_pipe_item_push(
			       &partial_pipe, partial_push_handler, &next
		       ) == 0) {
			// If push failed, try to free some items to make space.
			data_pipe_item_free(&partial_pipe, free_handler, NULL);
		}
	}

	printf("Partial producer pushed %lu items\n",
	       (unsigned long)PARTIAL_NUM_ITERATIONS);
	return NULL;
}

static void *
partial_consumer_thread(void *arg) {
	(void)arg;
	uintptr_t expected = 1;

	while (expected <= PARTIAL_NUM_ITERATIONS) {
		data_pipe_item_pop(
			&partial_pipe, partial_pop_handler, &expected
		);
	}

	printf("Partial consumer processed %lu items\n",
	       (unsigned long)expected - 1);
	return NULL;
}

static void *
producer_thread(void *arg) {
	(void)arg;

	for (uintptr_t i = 1; i <= NUM_ITERATIONS; i++) {
		uintptr_t val = i;
		while (data_pipe_item_push(&shared_pipe, push_handler, &val) ==
		       0) {
			// If push failed, try to free some items to make space.
			data_pipe_item_free(&shared_pipe, free_handler, NULL);
		}
	}

	printf("Producer pushed %lu items\n", (unsigned long)NUM_ITERATIONS);
	return NULL;
}

static void *
consumer_thread(void *arg) {
	(void)arg;
	uintptr_t expected = 1;

	while (expected <= NUM_ITERATIONS) {
		data_pipe_item_pop(&shared_pipe, pop_handler, &expected);
	}

	printf("Consumer processed %lu items\n", (unsigned long)expected - 1);
	return NULL;
}

int
main(void) {
	printf("=== TSAN test: real common/data_pipe.h ===\n");

	int ret = data_pipe_init(&shared_pipe, PIPE_SIZE_LOG2);
	assert(ret == 0);

	pthread_t prod, cons;
	pthread_create(&prod, NULL, producer_thread, NULL);
	pthread_create(&cons, NULL, consumer_thread, NULL);

	pthread_join(prod, NULL);
	pthread_join(cons, NULL);

	data_pipe_fini(&shared_pipe);

	printf("Done.\n");

	printf("=== TSAN test: partial-consume (retry) scenario ===\n");

	ret = data_pipe_init(&partial_pipe, PARTIAL_PIPE_SIZE_LOG2);
	assert(ret == 0);

	pthread_t partial_prod, partial_cons;
	pthread_create(&partial_prod, NULL, partial_producer_thread, NULL);
	pthread_create(&partial_cons, NULL, partial_consumer_thread, NULL);

	pthread_join(partial_prod, NULL);
	pthread_join(partial_cons, NULL);

	data_pipe_fini(&partial_pipe);

	printf("Done.\n");
	return 0;
}
