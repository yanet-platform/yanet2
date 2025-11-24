#pragma once

#include "../../dataplane/dataplane.h"

#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

/// Max number of workers working in yanet mock.
#define YANET_MOCK_MAX_WORKERS 8

////////////////////////////////////////////////////////////////////////////////

/// Mock of the single Yanet instance.
/// Uses controlplane API and mocks
/// dataplane workers on processing packets.
///
/// Works in single thread of the single process.
struct yanet_mock {
	struct dataplane_instance dataplane;

	// Null is arena was not allocated by mock,
	// so mock should not free arena.
	void *arena;

	// Shared memory where yanet lives in.
	void *shm;

	size_t workers_count;
	struct yanet_mock_worker workers[YANET_MOCK_MAX_WORKERS];
};

/// Returns 0 on success and -1 on error.
/// If arena is NULL, makes allocation.
int
yanet_mock_init(
	struct yanet_mock *mock,
	size_t cp_memory,
	size_t dp_memory,
	void *arena,
	size_t workers
);

int
yanet_mock_free(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

struct shm;

/// Shared memory where yanet lives in.
///
/// Memory is shared between yanet controlplane and
/// dataplane mock, which works in the same process thread.
struct shm *
yanet_mock_shm(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

/// Handle packets using worker number `worker_idx`.
struct packet_handle_result
yanet_mock_handle_packets(
	struct yanet_mock *mock, struct packet_list *packets, size_t worker_idx
);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_prepare_for_update(struct yanet_mock *mock);