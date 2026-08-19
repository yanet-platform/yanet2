#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#include "api/agent.h"
#include "api/counter.h"
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

// Verify that the readiness sequence (dp_storage_init -> cp_config_unlock ->
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

	cp_config_unlock(cp_config);
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
// sequence: dp_storage_init -> cp_config_unlock -> dp_config_mark_ready.
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
	cp_config_unlock(cp_config);
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
// the dataplane init sequence: dp_storage_init, system agent, cp_config_gen,
// unlock, mark_ready.
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
	struct agent *sys_agent =
		dp_system_agent_new(cp_config, dp_config, "dataplane");
	TEST_ASSERT_NOT_NULL(sys_agent, "dp_system_agent_new failed");

	yanet_error *setup_err = NULL;
	struct cp_config_gen *cp_config_gen =
		cp_config_gen_new(sys_agent, &setup_err);
	TEST_ASSERT_NOT_NULL(cp_config_gen, "cp_config_gen_new failed");
	SET_OFFSET_OF(&cp_config->cp_config_gen, cp_config_gen);

	dp_config->instance_count = 1;
	cp_config_unlock(cp_config);
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
	cp_config_unlock(cp_config);
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

	if (tests_failed != 0) {
		LOG(ERROR, "%zu/%zu tests failed", tests_failed, tests_count);
		return 1;
	}

	return 0;
}
