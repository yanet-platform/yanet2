#pragma once

#include "config.h"
#include "worker.h"
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

/// Max number of workers in the yanet mock.
#define YANET_MOCK_MAX_WORKERS 8

////////////////////////////////////////////////////////////////////////////////

/// Mock of the single Yanet instance.
/// Uses controlplane API and mocks
/// dataplane workers on processing packets.
///
/// Works in the single thread of the single process.
struct yanet_mock {
	// Controlplane config and Dataplane config of the single yanet instance
	struct cp_config *cp_config;
	struct dp_config *dp_config;

	// Null if arena was not allocated by mock,
	// so mock should not free arena.
	void *arena;

	// Shared memory where yanet lives in
	void *storage;

	// Current real time of the mock.
	struct timespec current_time;

	size_t worker_count;
	struct yanet_worker_mock workers[YANET_MOCK_MAX_WORKERS];
};

/// Returns 0 on success and -1 on error.
/// If arena is NULL, makes allocation.
int
yanet_mock_init(
	struct yanet_mock *mock, struct yanet_mock_config *config, void *arena
);

/// Free yanet mock.
/// Deallocates shared memory if `arena` passed to `yanet_mock_init` was NULL.
void
yanet_mock_free(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

/// Shared memory where yanet lives in.
///
/// Memory is shared between yanet controlplane and
/// dataplane mock, which works in the same process thread.
struct yanet_shm;

/// Get shared memory where yanet mock lives in.
struct yanet_shm *
yanet_mock_shm(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

/// Handle packets using worker number `worker_idx`.
struct packet_handle_result
yanet_mock_handle_packets(
	struct yanet_mock *mock, struct packet_list *packets, size_t worker_idx
);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_set_current_time(struct yanet_mock *mock, struct timespec *ts);

struct timespec
yanet_mock_current_time(struct yanet_mock *mock);