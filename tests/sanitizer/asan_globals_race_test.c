/*
 * The address sanitizer runtime registers globals safely while threads start.
 *
 * Go test binaries built with -asan register the globals of every package
 * during package init, while the Go runtime already starts threads. A
 * sanitizer runtime that serves both from one allocator under two locks
 * (llvm/llvm-project#87324) hands a globals list node and a new thread's
 * context the same memory and crashes random tests. This program races the
 * two for a few seconds and dies on such a runtime.
 */

#include <pthread.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

// The record the sanitizer runtime reads for each registered global.
struct asan_global {
	uintptr_t beg;
	uintptr_t size;
	uintptr_t size_with_redzone;
	const char *name;
	const char *module_name;
	uintptr_t has_dynamic_init;
	void *location;
	uintptr_t odr_indicator;
};

// The runtime entry point that instrumented code calls from its constructors,
// declared the way gcc declares its built-in.
void
__asan_register_globals(void *globals, intptr_t n);

#define GLOBALS_RACE_COUNT 64
#define GLOBALS_RACE_CREATORS 3

// The runtime keeps every registration, so the rounds stay bounded in memory;
// a racing runtime fails within a small fraction of them.
#define GLOBALS_RACE_ROUNDS 200000

// Each global has a 32-byte body and a 32-byte red zone.
static char globals_race_storage[GLOBALS_RACE_COUNT][64]
	__attribute__((aligned(64)));
static struct asan_global globals_race_globals[GLOBALS_RACE_COUNT];

static atomic_bool globals_race_stop;

static void *
globals_race_noop(void *arg) {
	return arg;
}

// Starts and joins threads until told to stop, so the runtime allocates
// thread contexts while the main thread registers globals.
static void *
globals_race_creator(void *arg) {
	(void)arg;
	while (!atomic_load(&globals_race_stop)) {
		pthread_t thread;
		if (pthread_create(&thread, NULL, globals_race_noop, NULL) ==
		    0) {
			pthread_join(thread, NULL);
		}
	}
	return NULL;
}

int
main(int argc, char **argv) {
	int seconds = argc > 1 ? atoi(argv[1]) : 10;

	// The same globals are registered again every round; the all-ones
	// indicator tells the runtime to skip its one definition rule check.
	for (int idx = 0; idx < GLOBALS_RACE_COUNT; idx++) {
		globals_race_globals[idx] = (struct asan_global){
			.beg = (uintptr_t)globals_race_storage[idx],
			.size = 32,
			.size_with_redzone = 64,
			.name = "globals_race_storage",
			.module_name = "asan_globals_race_test",
			.odr_indicator = UINTPTR_MAX,
		};
	}

	pthread_t creators[GLOBALS_RACE_CREATORS];
	for (int idx = 0; idx < GLOBALS_RACE_CREATORS; idx++) {
		if (pthread_create(
			    &creators[idx], NULL, globals_race_creator, NULL
		    ) != 0) {
			fprintf(stderr, "failed to start a thread creator\n");
			return EXIT_FAILURE;
		}
	}

	time_t deadline = time(NULL) + seconds;
	unsigned long rounds = 0;
	while (rounds < GLOBALS_RACE_ROUNDS && time(NULL) < deadline) {
		__asan_register_globals(
			globals_race_globals, GLOBALS_RACE_COUNT
		);
		rounds++;
	}

	atomic_store(&globals_race_stop, true);
	for (int idx = 0; idx < GLOBALS_RACE_CREATORS; idx++) {
		pthread_join(creators[idx], NULL);
	}
	printf("registered globals %lu times while threads started\n", rounds);
	return EXIT_SUCCESS;
}
