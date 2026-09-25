/*
 * Device-list reads keep their generation alive while copying unlocked.
 *
 * A reader pauses at a device snapshot allocation so a replacement can retire
 * its source generation before the remaining fields are copied. A failed
 * device allocation also verifies release on the error path.
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
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define DEVICE_LIST_AGENT_MEMORY (4u * 1024u * 1024u)
#define DEVICE_LIST_WAIT_SECONDS 10
#define DEVICE_LIST_NAME "snapshot-device"
#define DEVICE_LIST_PIPELINE "snapshot-pipeline"
#define DEVICE_LIST_OLD_WEIGHT 17
#define DEVICE_LIST_NEW_WEIGHT 29
#define DEVICE_INFO_ALLOCATION_SIZE                                            \
	(sizeof(struct cp_device_info) + sizeof(struct cp_device_pipeline_info))

struct allocation_gate {
	pthread_mutex_t mutex;
	pthread_cond_t cond;
	bool armed;
	bool reached;
	bool released;
	bool fail;
};

static struct allocation_gate device_info_gate = {
	.mutex = PTHREAD_MUTEX_INITIALIZER,
	.cond = PTHREAD_COND_INITIALIZER,
};

static _Thread_local bool control_device_info_read;

void *
__real_malloc(size_t size);

// Stop one selected allocation before the reader resumes source copying.
void *
__wrap_malloc(size_t size) {
	if (!control_device_info_read) {
		return __real_malloc(size);
	}

	pthread_mutex_lock(&device_info_gate.mutex);
	if (!device_info_gate.armed || size != DEVICE_INFO_ALLOCATION_SIZE) {
		pthread_mutex_unlock(&device_info_gate.mutex);
		return __real_malloc(size);
	}

	device_info_gate.armed = false;
	device_info_gate.reached = true;
	pthread_cond_broadcast(&device_info_gate.cond);
	while (!device_info_gate.released) {
		pthread_cond_wait(
			&device_info_gate.cond, &device_info_gate.mutex
		);
	}
	bool fail = device_info_gate.fail;
	pthread_mutex_unlock(&device_info_gate.mutex);

	return fail ? NULL : __real_malloc(size);
}

// Arm one allocation stop for a fresh device-list read.
static void
allocation_gate_arm(void) {
	pthread_mutex_lock(&device_info_gate.mutex);
	device_info_gate.armed = true;
	device_info_gate.reached = false;
	device_info_gate.released = false;
	device_info_gate.fail = false;
	pthread_mutex_unlock(&device_info_gate.mutex);
}

// Open the allocation stop and optionally reject the allocation.
static void
allocation_gate_release(bool fail) {
	pthread_mutex_lock(&device_info_gate.mutex);
	device_info_gate.fail = fail;
	device_info_gate.released = true;
	pthread_cond_broadcast(&device_info_gate.cond);
	pthread_mutex_unlock(&device_info_gate.mutex);
}

// Wait for the reader without allowing a missing stop to hang the test.
static bool
allocation_gate_wait(void) {
	struct timespec deadline;
	clock_gettime(CLOCK_REALTIME, &deadline);
	deadline.tv_sec += DEVICE_LIST_WAIT_SECONDS;

	pthread_mutex_lock(&device_info_gate.mutex);
	int rc = 0;
	while (!device_info_gate.reached && rc == 0) {
		rc = pthread_cond_timedwait(
			&device_info_gate.cond,
			&device_info_gate.mutex,
			&deadline
		);
	}
	bool reached = device_info_gate.reached;
	pthread_mutex_unlock(&device_info_gate.mutex);
	return reached;
}

// Install the pipeline referenced by every test device.
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
new_device(struct agent *agent, uint64_t weight, yanet_error **err) {
	struct cp_device_plain_config *config =
		cp_device_plain_config_new(DEVICE_LIST_NAME, 1, 0, err);
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

struct device_list_fixture {
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct cp_device *device;
};

// Create one isolated generation containing the weighted test device.
static int
device_list_fixture_init(
	struct yanet_shm *shm,
	const char *agent_name,
	struct device_list_fixture *fixture
) {
	yanet_error *err = NULL;
	memset(fixture, 0, sizeof(*fixture));
	fixture->agent = agent_attach(
		shm, 0, agent_name, DEVICE_LIST_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		fixture->agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	fixture->dp_config = agent_dp_config(fixture->agent);
	fixture->cp_config = ADDR_OF(&fixture->agent->cp_config);
	TEST_ASSERT_SUCCESS(
		install_empty_pipeline(fixture->dp_config, fixture->cp_config),
		"failed to install the test pipeline"
	);
	fixture->device =
		new_device(fixture->agent, DEVICE_LIST_OLD_WEIGHT, &err);
	TEST_ASSERT_NOT_NULL(
		fixture->device,
		"failed to build the test device: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_device *devices[] = {fixture->device};
	int rc = cp_config_update_devices(
		fixture->dp_config, fixture->cp_config, 1, devices, &err
	);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the test device: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Find the named test device and require its copied pipeline weight.
static bool
snapshot_has_weight(
	struct cp_device_list_info *list, uint64_t expected_weight
) {
	if (list == NULL) {
		return false;
	}

	for (uint64_t idx = 0; idx < list->device_count; ++idx) {
		struct cp_device_info *device =
			yanet_get_cp_device_info(list, idx);
		if (strncmp(device->name, DEVICE_LIST_NAME, sizeof(device->name)
		    ) != 0) {
			continue;
		}
		if (device->input_count != 1 || device->output_count != 0) {
			return false;
		}

		struct cp_device_pipeline_info *pipeline =
			yanet_get_cp_device_input_pipeline_info(device, 0);
		return pipeline != NULL &&
		       strncmp(pipeline->name,
			       DEVICE_LIST_PIPELINE,
			       sizeof(pipeline->name)) == 0 &&
		       pipeline->weight == expected_weight;
	}

	return false;
}

// Destroy a device only after its final generation reference is gone.
static bool
release_device(struct cp_device *device) {
	yanet_error *err = NULL;
	bool released = cp_device_plain_free(device, &err) == 0;
	yanet_error_free(err);
	return released;
}

struct device_list_reader_args {
	struct dp_config *dp_config;
	struct cp_device_list_info *list;
};

// Read one snapshot while exposing only its device allocation to the gate.
static void *
device_list_reader(void *arg) {
	struct device_list_reader_args *args =
		(struct device_list_reader_args *)arg;
	control_device_info_read = true;
	args->list = yanet_get_cp_device_list_info(args->dp_config);
	control_device_info_read = false;
	return NULL;
}

// Verifies that replacement proceeds while the retired snapshot stays alive.
static int
run_device_list_generation_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct device_list_fixture fixture;
	TEST_ASSERT_SUCCESS(
		device_list_fixture_init(shm, "device-list-race", &fixture),
		"failed to prepare the generation-race fixture"
	);

	struct cp_device *replacement =
		new_device(fixture.agent, DEVICE_LIST_NEW_WEIGHT, &err);
	TEST_ASSERT_NOT_NULL(
		replacement,
		"failed to build the replacement device: %s",
		err ? yanet_error_message(err) : "?"
	);

	allocation_gate_arm();
	struct device_list_reader_args reader_args = {
		.dp_config = fixture.dp_config,
	};
	pthread_t reader;
	int create_rc =
		pthread_create(&reader, NULL, device_list_reader, &reader_args);
	TEST_ASSERT_EQUAL(create_rc, 0, "failed to create reader thread");

	bool allocation_reached = allocation_gate_wait();
	bool lock_available = false;
	bool updated = false;
	bool pin_held = false;
	bool old_device_freed_early = false;
	yanet_error *update_err = NULL;

	if (allocation_reached) {
		lock_available = cp_config_try_lock(fixture.cp_config);
		if (lock_available) {
			cp_config_unlock(fixture.cp_config);
		}
	}
	if (lock_available) {
		struct cp_device *devices[] = {replacement};
		updated = cp_config_update_devices(
				  fixture.dp_config,
				  fixture.cp_config,
				  1,
				  devices,
				  &update_err
			  ) == 0;
	}
	if (updated) {
		yanet_error *pin_err = NULL;
		errno = 0;
		int free_rc = cp_device_plain_free(fixture.device, &pin_err);
		pin_held = free_rc == -1 && errno == EAGAIN;
		old_device_freed_early = free_rc == 0;
		yanet_error_free(pin_err);
	}

	allocation_gate_release(
		!allocation_reached || !lock_available || !updated || !pin_held
	);
	int join_rc = pthread_join(reader, NULL);
	if (join_rc != 0) {
		LOG(ERROR, "failed to join reader thread: %d", join_rc);
		abort();
	}

	bool old_snapshot =
		snapshot_has_weight(reader_args.list, DEVICE_LIST_OLD_WEIGHT);
	if (reader_args.list != NULL) {
		cp_device_list_info_free(reader_args.list);
	}

	struct cp_device_list_info *current_list =
		updated ? yanet_get_cp_device_list_info(fixture.dp_config)
			: NULL;
	bool new_snapshot =
		snapshot_has_weight(current_list, DEVICE_LIST_NEW_WEIGHT);
	if (current_list != NULL) {
		cp_device_list_info_free(current_list);
	}

	bool released_after_read = false;
	if (updated && !old_device_freed_early) {
		released_after_read = release_device(fixture.device);
	}
	if (!updated) {
		release_device(replacement);
	}
	yanet_error_free(update_err);
	yanet_error_free(err);

	TEST_ASSERT(
		allocation_reached, "reader did not reach the device allocation"
	);
	TEST_ASSERT(lock_available, "device copy kept the configuration lock");
	TEST_ASSERT(updated, "replacement failed while the reader was paused");
	TEST_ASSERT(
		pin_held, "retired generation was not pinned during the copy"
	);
	TEST_ASSERT(
		old_snapshot, "racing read did not return the old snapshot"
	);
	TEST_ASSERT(
		new_snapshot, "current read did not return the new snapshot"
	);
	TEST_ASSERT(
		released_after_read,
		"retired device stayed referenced after the read completed"
	);

	return TEST_SUCCESS;
}

// Verifies that a failed device allocation releases the pinned generation.
static int
run_device_info_allocation_failure_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct device_list_fixture fixture;
	TEST_ASSERT_SUCCESS(
		device_list_fixture_init(
			shm, "device-list-allocation", &fixture
		),
		"failed to prepare the allocation-failure fixture"
	);

	allocation_gate_arm();
	allocation_gate_release(true);
	control_device_info_read = true;
	struct cp_device_list_info *list =
		yanet_get_cp_device_list_info(fixture.dp_config);
	control_device_info_read = false;
	bool allocation_reached = allocation_gate_wait();
	bool read_failed = list == NULL;
	if (list != NULL) {
		cp_device_list_info_free(list);
	}

	struct cp_device *replacement =
		new_device(fixture.agent, DEVICE_LIST_NEW_WEIGHT, &err);
	bool replacement_created = replacement != NULL;
	bool updated = false;
	if (replacement_created) {
		struct cp_device *devices[] = {replacement};
		updated = cp_config_update_devices(
				  fixture.dp_config,
				  fixture.cp_config,
				  1,
				  devices,
				  &err
			  ) == 0;
	}
	bool generation_released = updated && release_device(fixture.device);
	if (replacement_created && !updated) {
		release_device(replacement);
	}
	yanet_error_free(err);

	TEST_ASSERT(
		allocation_reached, "reader did not reach the device allocation"
	);
	TEST_ASSERT(
		read_failed, "device allocation failure was not propagated"
	);
	TEST_ASSERT(replacement_created, "failed to build replacement device");
	TEST_ASSERT(updated, "failed to retire the failed-read generation");
	TEST_ASSERT(
		generation_released,
		"device allocation failure kept the generation pinned"
	);

	return TEST_SUCCESS;
}

typedef int (*device_list_scenario)(struct yanet_shm *shm);

// Run each scenario in fresh shared memory to isolate leaked generations.
static int
run_device_list_scenario(const char *name, device_list_scenario scenario) {
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
		LOG(ERROR, "%s: dataplane_ut_new failed", name);
		return TEST_FAILED;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		LOG(ERROR, "%s: dataplane_ut_shm returned NULL", name);
		dataplane_ut_free(ut);
		return TEST_FAILED;
	}

	int rc = scenario(shm);
	if (rc != TEST_SUCCESS) {
		LOG(ERROR, "%s failed", name);
	}
	dataplane_ut_free(ut);
	return rc;
}

int
main(void) {
	log_enable_name("info");

	if (run_device_list_scenario(
		    "generation race", run_device_list_generation_test
	    ) != TEST_SUCCESS) {
		return 1;
	}
	if (run_device_list_scenario(
		    "device allocation failure",
		    run_device_info_allocation_failure_test
	    ) != TEST_SUCCESS) {
		return 1;
	}
	return 0;
}
