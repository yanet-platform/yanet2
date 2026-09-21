/*
 * Device-list reads keep one configuration snapshot while copying unlocked.
 *
 * The reader pauses at each of two device allocations. A writer replaces both
 * after the first pause; the lock must remain available to it, and the retired
 * generation must stay alive through the second copy. The resulting list must
 * contain only the pre-swap data.
 */

#include "api/agent.h"
#include "api/info.h"

#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define DEVICE_LIST_AGENT_MEMORY (4u * 1024u * 1024u)
#define DEVICE_LIST_WAIT_SECONDS 10
#define DEVICE_LIST_NAME_A "snapshot-device-a"
#define DEVICE_LIST_NAME_B "snapshot-device-b"
#define DEVICE_LIST_PIPELINE "snapshot-pipeline"
#define DEVICE_LIST_OLD_WEIGHT 17
#define DEVICE_LIST_NEW_WEIGHT 29
#define DEVICE_LIST_PAUSE_COUNT 2

struct allocation_pause {
	pthread_mutex_t mutex;
	pthread_cond_t cond;
	size_t size;
	unsigned reached;
	unsigned released;
	bool failures[DEVICE_LIST_PAUSE_COUNT];
};

static struct allocation_pause device_info_pause = {
	.mutex = PTHREAD_MUTEX_INITIALIZER,
	.cond = PTHREAD_COND_INITIALIZER,
};

static _Thread_local bool pause_device_info_allocation;

void *
__real_malloc(size_t size);

void *
__wrap_malloc(size_t size) {
	if (!pause_device_info_allocation || size != device_info_pause.size) {
		return __real_malloc(size);
	}

	pthread_mutex_lock(&device_info_pause.mutex);
	if (device_info_pause.reached >= DEVICE_LIST_PAUSE_COUNT) {
		pthread_mutex_unlock(&device_info_pause.mutex);
		return __real_malloc(size);
	}
	unsigned pause_idx = device_info_pause.reached++;
	pthread_cond_broadcast(&device_info_pause.cond);
	while (device_info_pause.released <= pause_idx) {
		pthread_cond_wait(
			&device_info_pause.cond, &device_info_pause.mutex
		);
	}
	bool fail = device_info_pause.failures[pause_idx];
	pthread_mutex_unlock(&device_info_pause.mutex);

	return fail ? NULL : __real_malloc(size);
}

// Close both allocation stops and clear failure injection for a fresh read.
static void
allocation_pause_arm(size_t size) {
	pthread_mutex_lock(&device_info_pause.mutex);
	device_info_pause.size = size;
	device_info_pause.reached = 0;
	device_info_pause.released = 0;
	for (unsigned idx = 0; idx < DEVICE_LIST_PAUSE_COUNT; ++idx) {
		device_info_pause.failures[idx] = false;
	}
	pthread_mutex_unlock(&device_info_pause.mutex);
}

// Wait until the reader reaches the requested allocation stop.
//
// The bounded wait turns a missing stop into a test failure instead of a hang.
static bool
allocation_pause_wait(unsigned count) {
	struct timespec deadline;
	clock_gettime(CLOCK_REALTIME, &deadline);
	deadline.tv_sec += DEVICE_LIST_WAIT_SECONDS;

	pthread_mutex_lock(&device_info_pause.mutex);
	int rc = 0;
	while (device_info_pause.reached < count && rc == 0) {
		rc = pthread_cond_timedwait(
			&device_info_pause.cond,
			&device_info_pause.mutex,
			&deadline
		);
	}
	bool reached = device_info_pause.reached >= count;
	pthread_mutex_unlock(&device_info_pause.mutex);
	return reached;
}

// Open an allocation stop, optionally making that allocation fail.
static void
allocation_pause_release(unsigned count, bool fail) {
	pthread_mutex_lock(&device_info_pause.mutex);
	if (count > 0 && count <= DEVICE_LIST_PAUSE_COUNT) {
		device_info_pause.failures[count - 1] = fail;
	}
	if (device_info_pause.released < count) {
		device_info_pause.released = count;
	}
	pthread_cond_broadcast(&device_info_pause.cond);
	pthread_mutex_unlock(&device_info_pause.mutex);
}

// Install the pipeline referenced by both device generations.
static int
install_empty_pipeline(
	struct dp_config *dp_config, struct cp_config *cp_config
) {
	yanet_error *err = NULL;
	struct cp_pipeline_config *config =
		cp_pipeline_config_create(DEVICE_LIST_PIPELINE, 0);
	TEST_ASSERT_NOT_NULL(config, "failed to allocate pipeline config");

	struct cp_pipeline_config *configs[] = {config};
	int rc = cp_config_update_pipelines(
		dp_config, cp_config, 1, configs, &err
	);
	cp_pipeline_config_free(config);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install pipeline: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Build a plain device with one weighted input pipeline.
static struct cp_device *
new_device(
	struct agent *agent,
	const char *name,
	uint64_t weight,
	yanet_error **err
) {
	struct cp_device_plain_config *config =
		cp_device_plain_config_new(name, 1, 0, err);
	if (config == NULL) {
		return NULL;
	}

	if (cp_device_plain_config_set_input_pipeline(
		    config, 0, DEVICE_LIST_PIPELINE, weight
	    )) {
		cp_device_plain_config_free(config);
		return NULL;
	}

	struct cp_device *device = cp_device_plain_new(agent, config, err);
	cp_device_plain_config_free(config);
	return device;
}

// Accept only a complete two-device snapshot from one expected generation.
static bool
device_snapshot_has_weights(
	struct cp_device_list_info *list, uint64_t expected_weight
) {
	if (list == NULL) {
		return false;
	}

	bool found_a = false;
	bool found_b = false;
	for (uint64_t idx = 0; idx < list->device_count; ++idx) {
		struct cp_device_info *device =
			yanet_get_cp_device_info(list, idx);
		bool *found = NULL;
		if (!strncmp(
			    device->name,
			    DEVICE_LIST_NAME_A,
			    sizeof(device->name)
		    )) {
			found = &found_a;
		} else if (!strncmp(
				   device->name,
				   DEVICE_LIST_NAME_B,
				   sizeof(device->name)
			   )) {
			found = &found_b;
		} else {
			continue;
		}
		if (*found || device->input_count != 1 ||
		    device->output_count != 0) {
			return false;
		}

		struct cp_device_pipeline_info *pipeline =
			yanet_get_cp_device_input_pipeline_info(device, 0);
		if (pipeline == NULL ||
		    strncmp(pipeline->name,
			    DEVICE_LIST_PIPELINE,
			    sizeof(pipeline->name)) != 0 ||
		    pipeline->weight != expected_weight) {
			return false;
		}
		*found = true;
	}

	return found_a && found_b;
}

struct device_list_reader_args {
	struct dp_config *dp_config;
	struct cp_device_list_info *list;
};

// Read one snapshot with both device allocations controlled by the test gate.
static void *
device_list_reader(void *arg) {
	struct device_list_reader_args *args =
		(struct device_list_reader_args *)arg;
	pause_device_info_allocation = true;
	args->list = yanet_get_cp_device_list_info(args->dp_config);
	pause_device_info_allocation = false;
	return NULL;
}

// Verifies that a racing replacement cannot block or tear a device snapshot.
static int
run_device_list_generation_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm, 0, "device-list", DEVICE_LIST_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	TEST_ASSERT_SUCCESS(
		install_empty_pipeline(dp_config, cp_config),
		"failed to install the test pipeline"
	);

	struct cp_device *old_device_a = new_device(
		agent, DEVICE_LIST_NAME_A, DEVICE_LIST_OLD_WEIGHT, &err
	);
	TEST_ASSERT_NOT_NULL(
		old_device_a,
		"failed to build old device A: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *old_device_b = new_device(
		agent, DEVICE_LIST_NAME_B, DEVICE_LIST_OLD_WEIGHT, &err
	);
	TEST_ASSERT_NOT_NULL(
		old_device_b,
		"failed to build old device B: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *old_devices[] = {old_device_a, old_device_b};
	TEST_ASSERT_SUCCESS(
		cp_config_update_devices(
			dp_config, cp_config, 2, old_devices, &err
		),
		"failed to install old devices: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_device *new_device_a = new_device(
		agent, DEVICE_LIST_NAME_A, DEVICE_LIST_NEW_WEIGHT, &err
	);
	TEST_ASSERT_NOT_NULL(
		new_device_a,
		"failed to build new device A: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *new_device_b = new_device(
		agent, DEVICE_LIST_NAME_B, DEVICE_LIST_NEW_WEIGHT, &err
	);
	TEST_ASSERT_NOT_NULL(
		new_device_b,
		"failed to build new device B: %s",
		err ? yanet_error_message(err) : "?"
	);

	allocation_pause_arm(
		sizeof(struct cp_device_info) +
		sizeof(struct cp_device_pipeline_info)
	);
	struct device_list_reader_args reader_args = {
		.dp_config = dp_config,
	};
	pthread_t reader;
	int create_rc =
		pthread_create(&reader, NULL, device_list_reader, &reader_args);
	TEST_ASSERT_EQUAL(create_rc, 0, "failed to create reader thread");

	bool first_pause = allocation_pause_wait(1);
	bool lock_available = false;
	bool updated = false;
	bool first_pin_held = false;
	bool second_pause = false;
	bool second_pin_held = false;
	bool old_device_a_freed_early = false;
	bool old_device_b_freed_early = false;
	yanet_error *update_err = NULL;

	if (first_pause) {
		lock_available = cp_config_try_lock(cp_config);
		if (lock_available) {
			cp_config_unlock(cp_config);
		}
	}

	if (lock_available) {
		struct cp_device *new_devices[] = {new_device_a, new_device_b};
		updated = cp_config_update_devices(
				  dp_config,
				  cp_config,
				  2,
				  new_devices,
				  &update_err
			  ) == 0;
	}

	if (updated) {
		yanet_error *pin_err = NULL;
		errno = 0;
		int free_rc = cp_device_plain_free(old_device_a, &pin_err);
		first_pin_held = free_rc == -1 && errno == EAGAIN;
		old_device_a_freed_early = free_rc == 0;
		yanet_error_free(pin_err);
	}

	allocation_pause_release(
		1,
		!first_pause || !lock_available || !updated || !first_pin_held
	);

	if (first_pause && lock_available && updated && first_pin_held) {
		second_pause = allocation_pause_wait(2);
		if (second_pause) {
			yanet_error *pin_err = NULL;
			errno = 0;
			int free_rc =
				cp_device_plain_free(old_device_b, &pin_err);
			second_pin_held = free_rc == -1 && errno == EAGAIN;
			old_device_b_freed_early = free_rc == 0;
			yanet_error_free(pin_err);
		}
		allocation_pause_release(2, !second_pause || !second_pin_held);
	}
	int join_rc = pthread_join(reader, NULL);
	if (join_rc != 0) {
		LOG(ERROR, "failed to join reader thread: %d", join_rc);
		abort();
	}

	bool read_succeeded = reader_args.list != NULL;
	bool old_snapshot = device_snapshot_has_weights(
		reader_args.list, DEVICE_LIST_OLD_WEIGHT
	);
	if (reader_args.list != NULL) {
		cp_device_list_info_free(reader_args.list);
	}

	struct cp_device_list_info *current_list =
		updated ? yanet_get_cp_device_list_info(dp_config) : NULL;
	bool new_snapshot = device_snapshot_has_weights(
		current_list, DEVICE_LIST_NEW_WEIGHT
	);
	if (current_list != NULL) {
		cp_device_list_info_free(current_list);
	}

	bool old_device_a_released = false;
	if (updated && !old_device_a_freed_early) {
		yanet_error *release_err = NULL;
		old_device_a_released =
			cp_device_plain_free(old_device_a, &release_err) == 0;
		yanet_error_free(release_err);
	}
	bool old_device_b_released = false;
	if (updated && !old_device_b_freed_early) {
		yanet_error *release_err = NULL;
		old_device_b_released =
			cp_device_plain_free(old_device_b, &release_err) == 0;
		yanet_error_free(release_err);
	}
	if (!updated) {
		yanet_error *release_err_a = NULL;
		cp_device_plain_free(new_device_a, &release_err_a);
		yanet_error_free(release_err_a);
		yanet_error *release_err_b = NULL;
		cp_device_plain_free(new_device_b, &release_err_b);
		yanet_error_free(release_err_b);
	}

	yanet_error_free(update_err);
	yanet_error_free(err);

	TEST_ASSERT(first_pause, "reader did not reach the first device copy");
	TEST_ASSERT(
		lock_available,
		"device copy kept the configuration lock exclusively"
	);
	TEST_ASSERT(
		updated,
		"racing device replacement failed while the reader was paused"
	);
	TEST_ASSERT(
		first_pin_held,
		"retired generation was not pinned at the first device copy"
	);
	TEST_ASSERT(
		second_pause, "reader did not reach the second device copy"
	);
	TEST_ASSERT(
		second_pin_held,
		"retired generation was not pinned at the second device copy"
	);
	TEST_ASSERT(read_succeeded, "device-list read failed");
	TEST_ASSERT(
		old_snapshot, "device list mixed generations after the swap"
	);
	TEST_ASSERT(new_snapshot, "current device list missed the replacement");
	TEST_ASSERT(
		old_device_a_released && old_device_b_released,
		"retired devices stayed referenced after the read completed"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *devices_to_load[] = {"plain"};
	struct dataplane_ut_config config = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.devices_to_load = devices_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&config);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int rc = run_device_list_generation_test(shm);
	if (rc != TEST_SUCCESS) {
		LOG(ERROR, "run_device_list_generation_test failed");
	}

	dataplane_ut_free(ut);
	return rc == TEST_SUCCESS ? 0 : 1;
}
