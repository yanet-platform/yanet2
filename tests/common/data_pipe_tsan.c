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

	data_pipe_free(&shared_pipe);

	printf("Done.\n");
	return 0;
}
