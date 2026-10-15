#pragma once

#include <stdbool.h>
#include <stdint.h>

struct agent;
struct yanet_shm;

// Add one idle synchronous worker to a freshly bootstrapped storage fixture.
int
pdump_test_prepare_worker(struct yanet_shm *shm);

// Release the idle worker after all publication and cleanup has finished.
void
pdump_test_free_worker(struct yanet_shm *shm);

// Publish a deterministic timestamp for the fixture's only worker.
void
pdump_test_set_worker_time(struct agent *agent, uint64_t timestamp);

struct pdump_test_rate_snapshot {
	uint64_t rate_pps;
	uint64_t cost;
	uint64_t cap;
	uint64_t edt;
	bool has_bucket;
};

// Copy settled scalar settings while holding the publication lock.
int
pdump_test_read_rate(
	struct agent *agent,
	const char *name,
	struct pdump_test_rate_snapshot *snapshot
);
