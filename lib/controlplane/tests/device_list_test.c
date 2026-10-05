/*
 * Device, function and pipeline snapshots retain one generation while copying.
 *
 * A reader pauses after selecting its first item while a replacement changes
 * both items. Writers must proceed, each snapshot must remain consistent, and
 * successful reads and allocation failures must release their generation pins.
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
#define DEVICE_LIST_PEER "snapshot-peer"
#define DEVICE_LIST_COUNT 2
#define DEVICE_LIST_PIPELINE "snapshot-pipeline"
#define DEVICE_LIST_PIPELINE_PEER "snapshot-pipeline-peer"
#define DEVICE_LIST_FUNCTION "snapshot-function"
#define DEVICE_LIST_FUNCTION_PEER "snapshot-function-peer"
#define DEVICE_LIST_CHAIN "snapshot-chain"
#define DEVICE_LIST_OLD_WEIGHT 17
#define DEVICE_LIST_NEW_WEIGHT 29
#define DEVICE_INFO_ALLOCATION_SIZE                                            \
	(sizeof(struct cp_device_info) + sizeof(struct cp_device_pipeline_info))
#define FUNCTION_INFO_ALLOCATION_SIZE                                          \
	(sizeof(struct cp_function_info) + sizeof(struct cp_chain_info *))
#define PIPELINE_INFO_ALLOCATION_SIZE                                          \
	(sizeof(struct cp_pipeline_info) + sizeof(struct cp_function_info_id))

enum snapshot_reader_kind {
	SNAPSHOT_DEVICES,
	SNAPSHOT_FUNCTIONS,
	SNAPSHOT_PIPELINES,
};

struct allocation_gate {
	pthread_mutex_t mutex;
	pthread_cond_t cond;
	bool armed;
	bool reached;
	bool released;
	bool fail;
	size_t size;
	uint64_t skip;
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
	if (!device_info_gate.armed || size != device_info_gate.size) {
		pthread_mutex_unlock(&device_info_gate.mutex);
		return __real_malloc(size);
	}
	if (device_info_gate.skip != 0) {
		--device_info_gate.skip;
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

// Arm one selected allocation stop for the next snapshot read.
static void
allocation_gate_arm(size_t size, uint64_t skip) {
	pthread_mutex_lock(&device_info_gate.mutex);
	device_info_gate.armed = true;
	device_info_gate.reached = false;
	device_info_gate.released = false;
	device_info_gate.fail = false;
	device_info_gate.size = size;
	device_info_gate.skip = skip;
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

static const char *const device_list_names[] = {
	DEVICE_LIST_NAME, DEVICE_LIST_PEER
};

static const char *const function_list_names[] = {
	DEVICE_LIST_FUNCTION, DEVICE_LIST_FUNCTION_PEER
};

static const char *const pipeline_list_names[] = {
	DEVICE_LIST_PIPELINE, DEVICE_LIST_PIPELINE_PEER
};

// Install two functions whose single empty chains share a generation weight.
static int
install_snapshot_functions(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t weight
) {
	yanet_error *err = NULL;
	struct cp_function_config *functions[DEVICE_LIST_COUNT];
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		struct cp_chain_config *chain = cp_chain_config_create(
			DEVICE_LIST_CHAIN, 0, NULL, NULL
		);
		functions[idx] =
			cp_function_config_create(function_list_names[idx], 1);
		TEST_ASSERT_NOT_NULL(chain, "failed to allocate chain config");
		TEST_ASSERT_NOT_NULL(
			functions[idx], "failed to allocate function config"
		);
		TEST_ASSERT_SUCCESS(
			cp_function_config_set_chain(
				functions[idx], 0, chain, weight
			),
			"failed to set function chain"
		);
	}
	int rc = cp_config_update_functions(
		dp_config, cp_config, DEVICE_LIST_COUNT, functions, &err
	);
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		cp_function_config_free(functions[idx]);
	}
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install functions: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Install two pipelines referencing the selected function.
static int
install_snapshot_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *function_name
) {
	yanet_error *err = NULL;
	struct cp_pipeline_config *pipelines[DEVICE_LIST_COUNT];
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		pipelines[idx] =
			cp_pipeline_config_create(pipeline_list_names[idx], 1);
		TEST_ASSERT_NOT_NULL(
			pipelines[idx], "failed to allocate pipeline config"
		);
		TEST_ASSERT_SUCCESS(
			cp_pipeline_config_set_function(
				pipelines[idx], 0, function_name
			),
			"failed to set pipeline function"
		);
	}
	int rc = cp_config_update_pipelines(
		dp_config, cp_config, DEVICE_LIST_COUNT, pipelines, &err
	);
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		cp_pipeline_config_free(pipelines[idx]);
	}
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install pipelines: %s",
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

struct device_list_fixture {
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct cp_device *devices[DEVICE_LIST_COUNT];
};

// Create two entries in each list so a torn generation cannot appear valid.
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
		install_snapshot_functions(
			fixture->dp_config,
			fixture->cp_config,
			DEVICE_LIST_OLD_WEIGHT
		),
		"failed to install the test functions"
	);
	TEST_ASSERT_SUCCESS(
		install_snapshot_pipelines(
			fixture->dp_config,
			fixture->cp_config,
			DEVICE_LIST_FUNCTION
		),
		"failed to install the test pipelines"
	);
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		fixture->devices[idx] = new_device(
			fixture->agent,
			device_list_names[idx],
			DEVICE_LIST_OLD_WEIGHT,
			&err
		);
		TEST_ASSERT_NOT_NULL(
			fixture->devices[idx], "failed to build test device"
		);
	}
	int rc = cp_config_update_devices(
		fixture->dp_config,
		fixture->cp_config,
		DEVICE_LIST_COUNT,
		fixture->devices,
		&err
	);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the test devices: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Require both devices to come from the same weighted generation.
static bool
snapshot_has_weight(
	struct cp_device_list_info *list, uint64_t expected_weight
) {
	if (list == NULL) {
		return false;
	}

	uint64_t found = 0;
	for (uint64_t idx = 0; idx < list->device_count; ++idx) {
		struct cp_device_info *device =
			yanet_get_cp_device_info(list, idx);
		if (strcmp(device->name, DEVICE_LIST_NAME) != 0 &&
		    strcmp(device->name, DEVICE_LIST_PEER) != 0) {
			continue;
		}
		if (device->input_count != 1 || device->output_count != 0) {
			return false;
		}

		struct cp_device_pipeline_info *pipeline =
			yanet_get_cp_device_input_pipeline_info(device, 0);
		if (pipeline == NULL ||
		    strcmp(pipeline->name, DEVICE_LIST_PIPELINE) != 0 ||
		    pipeline->weight != expected_weight) {
			return false;
		}
		found |= strcmp(device->name, DEVICE_LIST_NAME) == 0 ? 1 : 2;
	}
	return found == 3;
}

// Read one of the three production snapshots through its public API.
static void *
read_snapshot(struct dp_config *dp_config, enum snapshot_reader_kind kind) {
	switch (kind) {
	case SNAPSHOT_DEVICES:
		return yanet_get_cp_device_list_info(dp_config);
	case SNAPSHOT_FUNCTIONS:
		return yanet_get_cp_function_list_info(dp_config);
	case SNAPSHOT_PIPELINES:
		return yanet_get_cp_pipeline_list_info(dp_config);
	}
	abort();
}

// Free the complete heap-side snapshot, including any nested copies.
static void
free_snapshot(void *list, enum snapshot_reader_kind kind) {
	if (list == NULL) {
		return;
	}
	switch (kind) {
	case SNAPSHOT_DEVICES:
		cp_device_list_info_free(list);
		return;
	case SNAPSHOT_FUNCTIONS:
		cp_function_list_info_free(list);
		return;
	case SNAPSHOT_PIPELINES:
		cp_pipeline_list_info_free(list);
		return;
	}
	abort();
}

// Require every selected item to match one complete expected generation.
static bool
snapshot_matches(void *list, enum snapshot_reader_kind kind, bool updated) {
	if (list == NULL) {
		return false;
	}
	uint64_t weight =
		updated ? DEVICE_LIST_NEW_WEIGHT : DEVICE_LIST_OLD_WEIGHT;
	if (kind == SNAPSHOT_DEVICES) {
		return snapshot_has_weight(list, weight);
	}
	uint64_t found = 0;
	if (kind == SNAPSHOT_FUNCTIONS) {
		struct cp_function_list_info *functions = list;
		if (functions->function_count != DEVICE_LIST_COUNT) {
			return false;
		}
		for (uint64_t idx = 0; idx < functions->function_count; ++idx) {
			struct cp_function_info *function =
				functions->functions[idx];
			if (strcmp(function->name, DEVICE_LIST_FUNCTION) != 0 &&
			    strcmp(function->name, DEVICE_LIST_FUNCTION_PEER) !=
				    0) {
				return false;
			}
			if (function->chain_count != 1 ||
			    strcmp(function->chains[0]->name,
				   DEVICE_LIST_CHAIN) != 0 ||
			    function->chains[0]->weight != weight ||
			    function->chains[0]->length != 0) {
				return false;
			}
			found |= strcmp(function->name, DEVICE_LIST_FUNCTION) ==
						 0
					 ? 1
					 : 2;
		}
	} else {
		struct cp_pipeline_list_info *pipelines = list;
		if (pipelines->count != DEVICE_LIST_COUNT) {
			return false;
		}
		const char *function_name = updated ? DEVICE_LIST_FUNCTION_PEER
						    : DEVICE_LIST_FUNCTION;
		for (uint64_t idx = 0; idx < pipelines->count; ++idx) {
			struct cp_pipeline_info *pipeline =
				pipelines->pipelines[idx];
			if (strcmp(pipeline->name, DEVICE_LIST_PIPELINE) != 0 &&
			    strcmp(pipeline->name, DEVICE_LIST_PIPELINE_PEER) !=
				    0) {
				return false;
			}
			if (pipeline->length != 1 ||
			    strcmp(pipeline->functions[0].name,
				   function_name) != 0) {
				return false;
			}
			found |= strcmp(pipeline->name, DEVICE_LIST_PIPELINE) ==
						 0
					 ? 1
					 : 2;
		}
	}
	return found == 3;
}

// Destroy a device only after its final generation reference is gone.
static bool
release_device(struct cp_device *device) {
	yanet_error *err = NULL;
	bool released = cp_device_plain_free(device, &err) == 0;
	yanet_error_free(err);
	return released;
}

struct snapshot_reader_args {
	struct dp_config *dp_config;
	enum snapshot_reader_kind kind;
	void *list;
};

// Expose only the selected reader's allocations to the synchronization gate.
static void *
snapshot_reader(void *arg) {
	struct snapshot_reader_args *args = arg;
	control_device_info_read = true;
	args->list = read_snapshot(args->dp_config, args->kind);
	control_device_info_read = false;
	return NULL;
}

// Verifies that a paused copy allows replacement and retains one generation.
static int
run_snapshot_generation_race(
	struct yanet_shm *shm, enum snapshot_reader_kind kind
) {
	yanet_error *err = NULL;
	struct device_list_fixture fixture;
	TEST_ASSERT_SUCCESS(
		device_list_fixture_init(shm, "snapshot-race", &fixture),
		"failed to prepare the generation-race fixture"
	);

	struct cp_device *replacements[DEVICE_LIST_COUNT];
	for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
		replacements[idx] = new_device(
			fixture.agent,
			device_list_names[idx],
			DEVICE_LIST_NEW_WEIGHT,
			&err
		);
		TEST_ASSERT_NOT_NULL(
			replacements[idx], "failed to build replacement device"
		);
	}
	const size_t allocation_sizes[] = {
		DEVICE_INFO_ALLOCATION_SIZE,
		FUNCTION_INFO_ALLOCATION_SIZE,
		PIPELINE_INFO_ALLOCATION_SIZE,
	};
	allocation_gate_arm(allocation_sizes[kind], 0);
	struct snapshot_reader_args reader_args = {
		.dp_config = fixture.dp_config, .kind = kind
	};
	pthread_t reader;
	int create_rc =
		pthread_create(&reader, NULL, snapshot_reader, &reader_args);
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
		int rc = TEST_SUCCESS;
		if (kind == SNAPSHOT_FUNCTIONS) {
			rc = install_snapshot_functions(
				fixture.dp_config,
				fixture.cp_config,
				DEVICE_LIST_NEW_WEIGHT
			);
		} else if (kind == SNAPSHOT_PIPELINES) {
			rc = install_snapshot_pipelines(
				fixture.dp_config,
				fixture.cp_config,
				DEVICE_LIST_FUNCTION_PEER
			);
		}
		if (rc == TEST_SUCCESS) {
			// Retired devices expose the generation's read pin.
			updated = cp_config_update_devices(
					  fixture.dp_config,
					  fixture.cp_config,
					  DEVICE_LIST_COUNT,
					  replacements,
					  &update_err
				  ) == 0;
		}
	}
	if (updated) {
		yanet_error *pin_err = NULL;
		errno = 0;
		int free_rc =
			cp_device_plain_free(fixture.devices[0], &pin_err);
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

	bool old_snapshot = snapshot_matches(reader_args.list, kind, false);
	free_snapshot(reader_args.list, kind);
	void *current_list =
		updated ? read_snapshot(fixture.dp_config, kind) : NULL;
	bool new_snapshot = snapshot_matches(current_list, kind, true);
	free_snapshot(current_list, kind);

	bool released_after_read = false;
	if (updated && !old_device_freed_early) {
		released_after_read = release_device(fixture.devices[0]) &&
				      release_device(fixture.devices[1]);
	}
	if (!updated) {
		for (uint64_t idx = 0; idx < DEVICE_LIST_COUNT; ++idx) {
			release_device(replacements[idx]);
		}
	}
	yanet_error_free(update_err);
	yanet_error_free(err);

	TEST_ASSERT(allocation_reached, "reader did not reach its allocation");
	TEST_ASSERT(
		lock_available, "snapshot copy kept the configuration lock"
	);
	TEST_ASSERT(updated, "replacement failed while the reader was paused");
	TEST_ASSERT(
		pin_held, "retired generation was not pinned during the copy"
	);
	TEST_ASSERT(
		old_snapshot,
		"racing read did not return the complete old snapshot"
	);
	TEST_ASSERT(
		new_snapshot,
		"current read did not return the complete new snapshot"
	);
	TEST_ASSERT(
		released_after_read,
		"retired device stayed referenced after the read completed"
	);
	return TEST_SUCCESS;
}

static int
run_device_list_generation_test(struct yanet_shm *shm) {
	return run_snapshot_generation_race(shm, SNAPSHOT_DEVICES);
}

static int
run_function_list_generation_test(struct yanet_shm *shm) {
	return run_snapshot_generation_race(shm, SNAPSHOT_FUNCTIONS);
}

static int
run_pipeline_list_generation_test(struct yanet_shm *shm) {
	return run_snapshot_generation_race(shm, SNAPSHOT_PIPELINES);
}

// Sample generation retention under the same lock as its mutations.
static uint64_t
snapshot_refcnt(struct cp_config *cp_config, struct cp_config_gen *generation) {
	cp_config_lock(cp_config);
	uint64_t refs = generation->refcnt;
	cp_config_unlock(cp_config);
	return refs;
}

// Verifies that complete and partially failed copies release every read pin.
static int
run_snapshot_allocation_failures_test(struct yanet_shm *shm) {
	struct device_list_fixture fixture;
	TEST_ASSERT_SUCCESS(
		device_list_fixture_init(shm, "snapshot-allocation", &fixture),
		"failed to prepare snapshot allocation fixture"
	);
	struct cp_config_gen *generation =
		ADDR_OF(&fixture.cp_config->cp_config_gen);
	for (enum snapshot_reader_kind kind = SNAPSHOT_DEVICES;
	     kind <= SNAPSHOT_PIPELINES;
	     ++kind) {
		void *list = read_snapshot(fixture.dp_config, kind);
		bool valid = snapshot_matches(list, kind, false);
		free_snapshot(list, kind);
		TEST_ASSERT(
			valid, "snapshot reader %d returned invalid data", kind
		);
		TEST_ASSERT_EQUAL(
			snapshot_refcnt(fixture.cp_config, generation),
			1,
			"snapshot reader %d leaked a pin after success",
			kind
		);
	}
	struct {
		const char *name;
		enum snapshot_reader_kind kind;
		size_t size;
		uint64_t skip;
	} cases[] = {
		{"device list",
		 SNAPSHOT_DEVICES,
		 sizeof(struct cp_device_list_info) +
			 generation->device_registry.registry.capacity *
				 sizeof(struct cp_device_info *),
		 0},
		{"first device",
		 SNAPSHOT_DEVICES,
		 DEVICE_INFO_ALLOCATION_SIZE,
		 0},
		{"second device",
		 SNAPSHOT_DEVICES,
		 DEVICE_INFO_ALLOCATION_SIZE,
		 1},
		{"function list",
		 SNAPSHOT_FUNCTIONS,
		 sizeof(struct cp_function_list_info) +
			 generation->function_registry.registry.capacity *
				 sizeof(struct cp_function_info *),
		 0},
		{"first function",
		 SNAPSHOT_FUNCTIONS,
		 FUNCTION_INFO_ALLOCATION_SIZE,
		 0},
		{"second function",
		 SNAPSHOT_FUNCTIONS,
		 FUNCTION_INFO_ALLOCATION_SIZE,
		 2},
		{"first chain",
		 SNAPSHOT_FUNCTIONS,
		 sizeof(struct cp_chain_info),
		 1},
		{"second chain",
		 SNAPSHOT_FUNCTIONS,
		 sizeof(struct cp_chain_info),
		 3},
		{"pipeline list",
		 SNAPSHOT_PIPELINES,
		 sizeof(struct cp_pipeline_list_info) +
			 generation->pipeline_registry.registry.capacity *
				 sizeof(struct cp_pipeline_info *),
		 0},
		{"first pipeline",
		 SNAPSHOT_PIPELINES,
		 PIPELINE_INFO_ALLOCATION_SIZE,
		 0},
		{"second pipeline",
		 SNAPSHOT_PIPELINES,
		 PIPELINE_INFO_ALLOCATION_SIZE,
		 1},
	};
	_Static_assert(
		FUNCTION_INFO_ALLOCATION_SIZE == sizeof(struct cp_chain_info),
		"function and chain allocations share the gate's skip sequence"
	);
	for (uint64_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		allocation_gate_arm(cases[idx].size, cases[idx].skip);
		allocation_gate_release(true);
		struct snapshot_reader_args args = {
			.dp_config = fixture.dp_config, .kind = cases[idx].kind
		};
		snapshot_reader(&args);
		bool failed = args.list == NULL;
		free_snapshot(args.list, cases[idx].kind);
		TEST_ASSERT(
			allocation_gate_wait(),
			"%s allocation was not reached",
			cases[idx].name
		);
		TEST_ASSERT(
			failed,
			"%s allocation error was not propagated",
			cases[idx].name
		);
		TEST_ASSERT_EQUAL(
			snapshot_refcnt(fixture.cp_config, generation),
			1,
			"%s allocation error leaked a generation pin",
			cases[idx].name
		);
	}
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
	struct {
		const char *name;
		device_list_scenario run;
	} scenarios[] = {
		{"device-list generation swap", run_device_list_generation_test
		},
		{"function-list generation swap",
		 run_function_list_generation_test},
		{"pipeline-list generation swap",
		 run_pipeline_list_generation_test},
		{"snapshot allocation failures",
		 run_snapshot_allocation_failures_test},
	};
	for (uint64_t idx = 0; idx < sizeof(scenarios) / sizeof(scenarios[0]);
	     ++idx) {
		if (run_device_list_scenario(
			    scenarios[idx].name, scenarios[idx].run
		    ) != TEST_SUCCESS) {
			return 1;
		}
	}
	return 0;
}
