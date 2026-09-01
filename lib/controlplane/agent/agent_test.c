#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#include "api/agent.h"
#include "api/counter.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

// Sizes chosen to be large enough for dp_storage_init internals but small
// enough to keep the test fast and heap-only (no hugepages needed).
#define TEST_DP_MEMORY (1 << 20)
#define TEST_CP_MEMORY (1 << 20)
#define TEST_STORAGE_SIZE (TEST_DP_MEMORY + TEST_CP_MEMORY)

// Publish a worker array on the instance, mirroring the registration the
// dataplane performs at startup.
//
// The array and the workers are allocated from the instance's own memory so
// the relative pointers a reader follows resolve the same way they do in a
// live segment.
static int
publish_workers(
	struct dp_config *dp_config, const uint64_t *times, uint64_t count
) {
	struct dp_worker **workers = (struct dp_worker **)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_worker *) * count
	);
	if (workers == NULL) {
		return -1;
	}

	for (uint64_t idx = 0; idx < count; ++idx) {
		struct dp_worker *worker = (struct dp_worker *)memory_balloc(
			&dp_config->memory_context, sizeof(struct dp_worker)
		);
		if (worker == NULL) {
			return -1;
		}

		memset(worker, 0, sizeof(struct dp_worker));
		worker->idx = idx;
		worker->current_time = times[idx];
		SET_OFFSET_OF(workers + idx, worker);
	}

	SET_OFFSET_OF(&dp_config->workers, workers);
	dp_config->worker_count = count;
	return 0;
}

// Initialise a storage segment and hand back its instance configuration.
static int
init_instance(void *storage, struct dp_config **dp_config) {
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		dp_config,
		&cp_config
	);
	if (rc != 0) {
		return -1;
	}

	(void)cp_config;
	return 0;
}

// Verify that an instance whose workers have not run yet reports no time
// rather than a value a caller could mistake for one.
//
// A caller that cannot tell "no time" from a time has no way to avoid
// falling back to the host clock, which is the comparison this accessor
// exists to remove.
static int
test_current_time_without_workers_reports_none() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	TEST_ASSERT(
		init_instance(storage, &dp_config) == 0,
		"dp_storage_init failed"
	);

	uint64_t time_ns = 0;
	int rc = dataplane_instance_current_time(dp_config, &time_ns);
	TEST_ASSERT(rc == -1, "an instance with no workers must report none");

	free(storage);
	return TEST_SUCCESS;
}

// Verify that an instance whose workers are registered but have published
// nothing reports no time.
static int
test_current_time_before_first_round_reports_none() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	TEST_ASSERT(
		init_instance(storage, &dp_config) == 0,
		"dp_storage_init failed"
	);

	const uint64_t times[] = {0, 0};
	TEST_ASSERT(
		publish_workers(dp_config, times, 2) == 0,
		"failed to publish workers"
	);

	uint64_t time_ns = 0;
	int rc = dataplane_instance_current_time(dp_config, &time_ns);
	TEST_ASSERT(rc == -1, "workers that have not run must report none");

	free(storage);
	return TEST_SUCCESS;
}

// Verify that the instance time is the latest one its workers published, and
// that a worker left behind does not hold it back.
//
// A worker stopped inside a driver call keeps publishing its last time
// forever; taking anything but the latest would freeze the instance's clock
// on that worker for as long as the process lives.
static int
test_current_time_reports_latest_worker() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	TEST_ASSERT(
		init_instance(storage, &dp_config) == 0,
		"dp_storage_init failed"
	);

	const uint64_t times[] = {700, 900, 100};
	TEST_ASSERT(
		publish_workers(dp_config, times, 3) == 0,
		"failed to publish workers"
	);

	uint64_t time_ns = 0;
	int rc = dataplane_instance_current_time(dp_config, &time_ns);
	TEST_ASSERT_SUCCESS(rc, "an instance with a running worker has a time");
	TEST_ASSERT_EQUAL(
		time_ns,
		900,
		"the instance time must be the latest a worker published"
	);

	free(storage);
	return TEST_SUCCESS;
}

// Verify that the instance time does not move backwards when the worker that
// last advanced it stops.
static int
test_current_time_does_not_regress_when_a_worker_stops() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	TEST_ASSERT(
		init_instance(storage, &dp_config) == 0,
		"dp_storage_init failed"
	);

	const uint64_t times[] = {900, 100};
	TEST_ASSERT(
		publish_workers(dp_config, times, 2) == 0,
		"failed to publish workers"
	);

	uint64_t before = 0;
	TEST_ASSERT_SUCCESS(
		dataplane_instance_current_time(dp_config, &before),
		"the instance must report a time"
	);

	// The lagging worker keeps running while the leader stays put.
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	struct dp_worker *lagging = ADDR_OF(workers + 1);
	lagging->current_time = 800;

	uint64_t after = 0;
	TEST_ASSERT_SUCCESS(
		dataplane_instance_current_time(dp_config, &after),
		"the instance must still report a time"
	);
	TEST_ASSERT(
		after >= before, "the instance time must not move backwards"
	);
	TEST_ASSERT_EQUAL(after, 900, "the stopped worker still bounds it");

	free(storage);
	return TEST_SUCCESS;
}

// Verify that agent_attach returns NULL with a non-NULL error when the
// shared memory segment is zeroed (simulating the pre-init startup race).
static int
test_attach_zeroed_segment_returns_error() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct yanet_shm shm = {.base = storage, .size = TEST_STORAGE_SIZE};

	yanet_error *err = NULL;
	struct agent *result = agent_attach(&shm, 0, "test-agent", 4096, &err);

	TEST_ASSERT_NULL(
		result, "agent_attach on zeroed segment must return NULL"
	);
	TEST_ASSERT_NOT_NULL(
		err, "agent_attach on zeroed segment must set an error"
	);

	yanet_error_free(err);
	free(storage);
	return TEST_SUCCESS;
}

// Verify that the readiness sequence (dp_storage_init ->
// dp_config_mark_ready) leaves ready_magic set and the cp_config offset
// pointer navigable. This confirms the attach gate will open for a correctly
// initialised segment.
static int
test_initialised_segment_magic_and_cp_config_valid() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	(void)cp_config;
	dp_config_mark_ready(dp_config);

	// The release store in dp_config_mark_ready must be visible once we
	// load with acquire ordering.
	uint64_t magic =
		__atomic_load_n(&dp_config->ready_magic, __ATOMIC_ACQUIRE);
	TEST_ASSERT(
		magic == DP_CONFIG_READY_MAGIC,
		"dp_config_mark_ready must write DP_CONFIG_READY_MAGIC"
	);

	// Confirm the cp_config offset pointer resolves to a non-NULL address,
	// meaning ADDR_OF in agent_attach won't crash after passing the gate.
	struct cp_config *resolved = ADDR_OF(&dp_config->cp_config);
	TEST_ASSERT_NOT_NULL(
		resolved, "cp_config offset must resolve to a non-NULL address"
	);
	TEST_ASSERT(
		resolved == cp_config,
		"resolved cp_config must match the value returned by "
		"dp_storage_init"
	);

	free(storage);
	return TEST_SUCCESS;
}

// Verify that agent_dp_config_ready returns 0 on a zeroed segment, where the
// ready_magic field has never been written.
static int
test_ready_predicate_zeroed_segment_returns_false() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct yanet_shm shm = {.base = storage, .size = TEST_STORAGE_SIZE};
	int ready = agent_dp_config_ready(&shm, 0);
	TEST_ASSERT(ready == 0, "predicate must return 0 on a zeroed segment");

	free(storage);
	return TEST_SUCCESS;
}

// Verify that agent_dp_config_ready returns 1 after the full readiness
// sequence: dp_storage_init -> dp_config_mark_ready.
// Also sets instance_count so the instance_idx bound check in the predicate
// passes.
static int
test_ready_predicate_initialised_segment_returns_true() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	dp_config->instance_count = 1;
	(void)cp_config;
	dp_config_mark_ready(dp_config);

	struct yanet_shm shm = {.base = storage, .size = TEST_STORAGE_SIZE};
	int ready = agent_dp_config_ready(&shm, 0);
	TEST_ASSERT(
		ready == 1,
		"predicate must return 1 after the full readiness sequence"
	);

	free(storage);
	return TEST_SUCCESS;
}

// Verify that agent_attach succeeds on a fully initialised segment. This
// proves the attach gate opens once the instance is marked ready. Mirrors
// the dataplane init sequence: dp_storage_init, system agent and
// cp_config_gen under the configuration lock, mark_ready.
static int
test_attach_initialised_segment_succeeds() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	// A system agent and cp_config_gen are required before agent_attach
	// can succeed — agent_attach reads cp_config_gen->gen after locking.
	cp_config_lock(cp_config);
	struct agent *sys_agent =
		dp_system_agent_new(cp_config, dp_config, "dataplane");
	TEST_ASSERT_NOT_NULL(sys_agent, "dp_system_agent_new failed");

	yanet_error *setup_err = NULL;
	struct cp_config_gen *cp_config_gen =
		cp_config_gen_new(sys_agent, &setup_err);
	TEST_ASSERT_NOT_NULL(cp_config_gen, "cp_config_gen_new failed");
	SET_OFFSET_OF(&cp_config->cp_config_gen, cp_config_gen);
	cp_config_unlock(cp_config);

	dp_config->instance_count = 1;
	dp_config_mark_ready(dp_config);

	struct yanet_shm shm = {.base = storage, .size = TEST_STORAGE_SIZE};
	yanet_error *err = NULL;
	struct agent *result = agent_attach(&shm, 0, "test-agent", 4096, &err);

	TEST_ASSERT_NOT_NULL(
		result, "agent_attach must succeed on an initialised segment"
	);
	TEST_ASSERT_NULL(err, "agent_attach must not set an error on success");

	free(storage);
	return TEST_SUCCESS;
}

// Verify that yanet_get_port_counters returns NULL rather than faulting
// when the port count is published but the port counters array offset is
// not, reproducing the two-store publish race.
static int
test_get_port_counters_unpublished_array_returns_null() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	dp_config->port_count = 1;

	struct port_counter_group_list *groups =
		yanet_get_port_counters(dp_config);
	TEST_ASSERT_NULL(
		groups,
		"yanet_get_port_counters must return NULL when the port "
		"count is set but the counters array offset is not"
	);

	free(storage);
	return TEST_SUCCESS;
}

// Verify that yanet_get_port_counters returns a non-NULL, zero-length list
// for a zero port count instead of treating an unset array offset as an
// error.
static int
test_get_port_counters_zero_port_count_returns_empty_list() {
	void *storage = calloc(1, TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		storage,
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	struct port_counter_group_list *groups =
		yanet_get_port_counters(dp_config);
	TEST_ASSERT_NOT_NULL(
		groups,
		"yanet_get_port_counters must return a non-NULL list for a "
		"zero port count"
	);
	TEST_ASSERT_EQUAL(groups->port_count, 0, "port count must be zero");

	yanet_port_counter_group_list_free(groups);
	free(storage);
	return TEST_SUCCESS;
}

// Reports whether the page at the given address is still mapped in this
// process.
//
// Synchronising an unmapped range fails with ENOMEM, so a successful call
// proves the page is still there. Probed right after the detach, before
// anything else could reuse the address range.
static int
page_mapped(void *addr) {
	long page_size = sysconf(_SC_PAGESIZE);
	return msync(addr, (size_t)page_size, MS_ASYNC) == 0;
}

// Attaches to a fresh zero-filled storage file of the given size, the state
// the dataplane leaves the storage in before it writes the header.
//
// The file is unlinked as soon as it is attached, so a failing assertion
// never leaves it behind; the mapping outlives the directory entry. Returns
// NULL if the file could not be created or attached.
static struct yanet_shm *
attach_storage_file(size_t size) {
	char path[] = "/tmp/yanet-shm-test-XXXXXX";
	int fd = mkstemp(path);
	if (fd == -1) {
		return NULL;
	}
	int rc = ftruncate(fd, (off_t)size);
	close(fd);
	if (rc != 0) {
		unlink(path);
		return NULL;
	}
	struct yanet_shm *shm = yanet_shm_attach(path);
	unlink(path);
	return shm;
}

// Verify that detaching a segment whose header the dataplane has not written
// yet releases the whole mapping instead of failing, which is the state the
// director's readiness backoff attaches in.
static int
test_detach_uninitialised_segment_releases_mapping() {
	struct yanet_shm *shm = attach_storage_file(TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(shm, "attach to a zeroed storage file failed");

	uint8_t *base = (uint8_t *)yanet_shm_dp_config(shm, 0);
	long page_size = sysconf(_SC_PAGESIZE);
	TEST_ASSERT(page_mapped(base), "attach must map the segment");

	errno = 0;
	int rc = yanet_shm_detach(shm);
	TEST_ASSERT(
		rc == 0,
		"detach of an uninitialised segment must succeed, got %s",
		strerror(errno)
	);
	TEST_ASSERT(!page_mapped(base), "detach must unmap the first page");
	TEST_ASSERT(
		!page_mapped(base + TEST_STORAGE_SIZE - page_size),
		"detach must unmap the last page"
	);
	return TEST_SUCCESS;
}

// Verify that detaching releases what was mapped even when the header
// understates the segment, as it does while the dataplane is still writing
// it: a detach that trusted the header would leave the tail mapped.
static int
test_detach_understated_header_releases_whole_mapping() {
	struct yanet_shm *shm = attach_storage_file(2 * TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(shm, "attach to a zeroed storage file failed");

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	dp_config->instance_count = 1;
	dp_config->storage_size = TEST_STORAGE_SIZE;

	uint8_t *base = (uint8_t *)dp_config;
	long page_size = sysconf(_SC_PAGESIZE);
	errno = 0;
	int rc = yanet_shm_detach(shm);
	TEST_ASSERT(rc == 0, "detach must succeed, got %s", strerror(errno));
	TEST_ASSERT(
		!page_mapped(base + TEST_STORAGE_SIZE),
		"detach must unmap the part the header does not cover"
	);
	TEST_ASSERT(
		!page_mapped(base + 2 * TEST_STORAGE_SIZE - page_size),
		"detach must unmap the last page"
	);
	return TEST_SUCCESS;
}

// Verify that detaching a fully initialised, file-backed segment still
// releases exactly the mapping.
static int
test_detach_initialised_segment_releases_mapping() {
	struct yanet_shm *shm = attach_storage_file(TEST_STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(shm, "attach to a zeroed storage file failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0,
		0,
		yanet_shm_dp_config(shm, 0),
		TEST_DP_MEMORY,
		TEST_CP_MEMORY,
		&dp_config,
		&cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");
	dp_config->instance_count = 1;
	dp_config_mark_ready(dp_config);

	uint8_t *base = (uint8_t *)dp_config;
	long page_size = sysconf(_SC_PAGESIZE);
	errno = 0;
	rc = yanet_shm_detach(shm);
	TEST_ASSERT(rc == 0, "detach must succeed, got %s", strerror(errno));
	TEST_ASSERT(!page_mapped(base), "detach must unmap the first page");
	TEST_ASSERT(
		!page_mapped(base + TEST_STORAGE_SIZE - page_size),
		"detach must unmap the last page"
	);
	return TEST_SUCCESS;
}

// Allocates zeroed storage for an extend test.
static void *
extend_storage_alloc(size_t size) {
	return calloc(1, size);
}

static int
extend_env_init(
	void *storage, size_t cp_memory, struct cp_config **res_cp_config
) {
	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	if (dp_storage_init(
		    0,
		    0,
		    storage,
		    TEST_DP_MEMORY,
		    cp_memory,
		    &dp_config,
		    &cp_config
	    ) != 0) {
		return -1;
	}

	cp_config_lock(cp_config);

	struct agent *sys_agent =
		dp_system_agent_new(cp_config, dp_config, "dataplane");
	if (sys_agent == NULL) {
		return -1;
	}

	yanet_error *err = NULL;
	struct cp_config_gen *config_gen = cp_config_gen_new(sys_agent, &err);
	if (config_gen == NULL) {
		yanet_error_free(err);
		return -1;
	}
	SET_OFFSET_OF(&cp_config->cp_config_gen, config_gen);
	cp_config_unlock(cp_config);

	dp_config->instance_count = 1;
	dp_config_mark_ready(dp_config);

	*res_cp_config = cp_config;
	return 0;
}

static size_t
cp_pool_free_size(struct cp_config *cp_config) {
	return block_allocator_free_size(&cp_config->block_allocator);
}

static size_t
agent_reserved_size(struct agent *agent) {
	struct agent_arena *arenas = ADDR_OF(&agent->arenas);
	size_t size = 0;
	for (uint64_t idx = 0; idx < agent->arena_count; ++idx) {
		size += arenas[idx].size;
	}
	return size;
}

static void
extend_agent_release(struct agent *agent, struct cp_config *cp_config) {
	cp_config_lock(cp_config);
	agent_cleanup(agent);
	cp_config_unlock(cp_config);
}

// Verify that growing an agent adds usable capacity, records the new arena
// and publishes the new limit, all while staying inside a single chunk.
static int
test_extend_grows_agent_capacity() {
	void *storage = extend_storage_alloc(1 << 25);
	TEST_ASSERT_NOT_NULL(storage, "storage allocation failed");

	struct cp_config *cp_config = NULL;
	int rc = extend_env_init(storage, 1 << 24, &cp_config);
	TEST_ASSERT(rc == 0, "storage setup failed");

	struct yanet_shm shm = {.base = storage, .size = 1 << 25};
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(&shm, 0, "extend", 1 << 12, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	TEST_ASSERT_EQUAL(
		agent_reserved_size(agent),
		(size_t)(1u << 12),
		"attach must record the requested capacity"
	);

	void *block = memory_balloc(&agent->memory_context, 256);
	TEST_ASSERT_NOT_NULL(block, "the attached capacity must be usable");
	memory_bfree(&agent->memory_context, block, 256);

	block = memory_balloc(&agent->memory_context, 1 << 13);
	TEST_ASSERT_NULL(block, "agent allocated more memory than it has");

	rc = agent_extend(agent, 1u << 20, &err);
	TEST_ASSERT(rc == 0, "growing an attached agent must succeed");
	TEST_ASSERT_NULL(err, "a successful extend must not set an error");

	TEST_ASSERT_EQUAL(
		agent_memory_limit(agent),
		(uint64_t)((1u << 12) + (1u << 20)),
		"the reported limit must follow a successful extend"
	);
	TEST_ASSERT_EQUAL(
		agent_reserved_size(agent),
		(size_t)((1u << 12) + (1u << 20)),
		"the recorded arenas must add up to the new limit"
	);

	// The added memory is split along its own alignment, so the largest
	// usable block stays well below the growth that was requested.
	block = memory_balloc(&agent->memory_context, 1 << 14);
	TEST_ASSERT_NOT_NULL(block, "the added capacity must be usable");
	memory_bfree(&agent->memory_context, block, 1 << 14);

	extend_agent_release(agent, cp_config);
	free(storage);
	return TEST_SUCCESS;
}

static int
test_extend_rejects_borrowing_agent() {
	void *storage = extend_storage_alloc(1 << 25);
	TEST_ASSERT_NOT_NULL(storage, "storage allocation failed");

	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	int rc = dp_storage_init(
		0, 0, storage, TEST_DP_MEMORY, 1u << 22, &dp_config, &cp_config
	);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	cp_config_lock(cp_config);
	struct agent *sys_agent =
		dp_system_agent_new(cp_config, dp_config, "dataplane");
	TEST_ASSERT_NOT_NULL(sys_agent, "dp_system_agent_new failed");
	cp_config_unlock(cp_config);

	yanet_error *err = NULL;
	rc = agent_extend(sys_agent, 1u << 20, &err);
	TEST_ASSERT(rc == -1, "a borrowing agent must not be extended");
	TEST_ASSERT_NOT_NULL(err, "a rejected extend must set an error");
	TEST_ASSERT_EQUAL(
		sys_agent->arena_count,
		(uint64_t)0,
		"a rejected extend must not record an arena"
	);
	TEST_ASSERT_EQUAL(
		sys_agent->memory_limit,
		(uint64_t)0,
		"a rejected extend must not move the limit"
	);
	yanet_error_free(err);

	free(storage);
	return TEST_SUCCESS;
}

// Verify that a request the controlplane pool cannot satisfy leaves the
// agent, its capacity and its reported limit exactly as they were.
static int
test_extend_rolls_back_on_exhausted_pool() {
	void *storage = extend_storage_alloc(1 << 25);
	TEST_ASSERT_NOT_NULL(storage, "storage allocation failed");

	struct cp_config *cp_config = NULL;
	int rc = extend_env_init(storage, 1 << 19, &cp_config);
	TEST_ASSERT(rc == 0, "storage setup failed");

	struct yanet_shm shm = {.base = storage, .size = 1 << 25};
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(&shm, 0, "extend", 4096, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	uint64_t arena_count = agent->arena_count;
	uint64_t memory_limit = agent->memory_limit;
	size_t free_before = block_allocator_free_size(&agent->block_allocator);
	size_t pool_before = cp_pool_free_size(cp_config);

	rc = agent_extend(agent, 1 << 20, &err);
	TEST_ASSERT(rc == -1, "an unsatisfiable extend must fail");
	TEST_ASSERT_NOT_NULL(err, "a failed extend must set an error");
	yanet_error_free(err);
	err = NULL;

	TEST_ASSERT_EQUAL(
		agent->arena_count, arena_count, "no arena must be recorded"
	);
	TEST_ASSERT_EQUAL(
		agent->memory_limit, memory_limit, "the limit must not move"
	);
	TEST_ASSERT_EQUAL(
		block_allocator_free_size(&agent->block_allocator),
		free_before,
		"the capacity must not move"
	);
	TEST_ASSERT_EQUAL(
		cp_pool_free_size(cp_config),
		pool_before,
		"a failed request must return everything it took"
	);

	// The agent must still be serviceable after the failure.
	void *block = memory_balloc(&agent->memory_context, 256);
	TEST_ASSERT_NOT_NULL(block, "the agent must survive a failed extend");
	memory_bfree(&agent->memory_context, block, 256);

	extend_agent_release(agent, cp_config);
	free(storage);
	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("error");

	size_t tests_count = 0;
	size_t tests_failed = 0;

	++tests_count;
	if (test_attach_zeroed_segment_returns_error() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_attach_zeroed_segment_returns_error failed");
	}

	++tests_count;
	if (test_initialised_segment_magic_and_cp_config_valid() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_initialised_segment_magic_and_cp_config_valid failed"
		);
	}

	++tests_count;
	if (test_ready_predicate_zeroed_segment_returns_false() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_ready_predicate_zeroed_segment_returns_false failed");
	}

	++tests_count;
	if (test_ready_predicate_initialised_segment_returns_true() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_ready_predicate_initialised_segment_returns_true "
		    "failed");
	}

	++tests_count;
	if (test_attach_initialised_segment_succeeds() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_attach_initialised_segment_succeeds failed");
	}

	++tests_count;
	if (test_get_port_counters_unpublished_array_returns_null() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_get_port_counters_unpublished_array_returns_null "
		    "failed");
	}

	++tests_count;
	if (test_get_port_counters_zero_port_count_returns_empty_list() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_get_port_counters_zero_port_count_returns_empty_list "
		    "failed");
	}

	++tests_count;
	if (test_detach_uninitialised_segment_releases_mapping() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_detach_uninitialised_segment_releases_mapping failed"
		);
	}

	++tests_count;
	if (test_detach_understated_header_releases_whole_mapping() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_detach_understated_header_releases_whole_mapping "
		    "failed");
	}

	++tests_count;
	if (test_detach_initialised_segment_releases_mapping() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_detach_initialised_segment_releases_mapping failed");
	}

	++tests_count;
	if (test_extend_grows_agent_capacity() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_extend_grows_agent_capacity failed");
	}

	++tests_count;
	if (test_extend_rejects_borrowing_agent() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_extend_rejects_borrowing_agent failed");
	}

	++tests_count;
	if (test_extend_rolls_back_on_exhausted_pool() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_extend_rolls_back_on_exhausted_pool failed");
	}

	++tests_count;
	if (test_current_time_without_workers_reports_none() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_current_time_without_workers_reports_none failed");
	}

	++tests_count;
	if (test_current_time_before_first_round_reports_none() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_current_time_before_first_round_reports_none failed");
	}

	++tests_count;
	if (test_current_time_reports_latest_worker() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_current_time_reports_latest_worker failed");
	}

	++tests_count;
	if (test_current_time_does_not_regress_when_a_worker_stops() !=
	    TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR,
		    "test_current_time_does_not_regress_when_a_worker_stops "
		    "failed");
	}

	if (tests_failed != 0) {
		LOG(ERROR, "%zu/%zu tests failed", tests_failed, tests_count);
		return 1;
	}

	return 0;
}
