/*
 * Whole-structure snapshots read back the surviving configuration.
 *
 * A restarted control-plane process owns nothing in memory, so its only
 * view of the running configuration is what the pinned generation holds:
 * the snapshot must report every module with the agent that published it,
 * the functions, pipelines and devices together, and provenance must
 * follow ownership when a later agent replaces a module the earlier one
 * left behind.
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

#include "modules/decap/api/controlplane.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define STRUCTURE_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)
#define STRUCTURE_TEST_AGENT "structure-owner"
#define STRUCTURE_TEST_MODULE "structure-module"
#define STRUCTURE_TEST_FUNCTION "structure-function"
#define STRUCTURE_TEST_CHAIN "structure-chain"
#define STRUCTURE_TEST_PIPELINE "structure-pipeline"
#define STRUCTURE_TEST_DEVICE "structure-port"
#define STRUCTURE_TEST_WEIGHT 13

// Install one decap module owned by the agent.
static int
install_module(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
) {
	yanet_error *err = NULL;
	struct cp_module *module = decap_module_config_new(agent, name, &err);
	if (module == NULL) {
		yanet_error_free(err);
		TEST_ASSERT(false, "failed to build the module config");
	}

	int rc = cp_config_update_modules(
		dp_config, cp_config, 1, &module, &err
	);
	if (rc != 0) {
		yanet_error *free_err = NULL;
		decap_module_config_free(module, &free_err);
		yanet_error_free(free_err);
	}
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the module: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Install one function whose single empty chain carries the weight.
static int
install_function(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t weight
) {
	yanet_error *err = NULL;
	struct cp_chain_config *chain =
		cp_chain_config_create(STRUCTURE_TEST_CHAIN, 0, NULL, NULL);
	TEST_ASSERT_NOT_NULL(chain, "failed to allocate the chain config");

	struct cp_function_config *function =
		cp_function_config_create(STRUCTURE_TEST_FUNCTION, 1);
	TEST_ASSERT_NOT_NULL(
		function, "failed to allocate the function config"
	);
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(function, 0, chain, weight),
		"failed to set the function chain"
	);

	int rc = cp_config_update_functions(
		dp_config, cp_config, 1, &function, &err
	);
	cp_function_config_free(function);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the function: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Install one pipeline running the single function.
static int
install_pipeline(struct dp_config *dp_config, struct cp_config *cp_config) {
	yanet_error *err = NULL;
	struct cp_pipeline_config *pipeline =
		cp_pipeline_config_create(STRUCTURE_TEST_PIPELINE, 1);
	TEST_ASSERT_NOT_NULL(
		pipeline, "failed to allocate the pipeline config"
	);
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(
			pipeline, 0, STRUCTURE_TEST_FUNCTION
		),
		"failed to set the pipeline function"
	);

	int rc = cp_config_update_pipelines(
		dp_config, cp_config, 1, &pipeline, &err
	);
	cp_pipeline_config_free(pipeline);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the pipeline: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Install one plain device running the pipeline on its input side.
static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config
) {
	yanet_error *err = NULL;
	struct cp_device_plain_config *config =
		cp_device_plain_config_new(STRUCTURE_TEST_DEVICE, 1, 0, &err);
	TEST_ASSERT_NOT_NULL(
		config,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_input_pipeline(
			config,
			0,
			STRUCTURE_TEST_PIPELINE,
			STRUCTURE_TEST_WEIGHT
		),
		"failed to set the device input pipeline"
	);

	struct cp_device *device = cp_device_plain_new(agent, config, &err);
	cp_device_plain_config_free(config);
	TEST_ASSERT_NOT_NULL(
		device,
		"device new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	int rc = cp_config_update_devices(
		dp_config, cp_config, 1, &device, &err
	);
	TEST_ASSERT_SUCCESS(
		rc,
		"failed to install the device: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Find the single module of the snapshot and return its info.
static struct cp_module_info *
snapshot_module(struct cp_structure_info *structure) {
	if (structure->modules == NULL ||
	    structure->modules->module_count != 1) {
		return NULL;
	}
	return yanet_get_cp_module_info(structure->modules, 0);
}

// Find the installed device by name; the registry also holds the
// predefined port topology devices, which are not ours to describe.
static struct cp_device_info *
snapshot_device(struct cp_structure_info *structure) {
	if (structure->devices == NULL) {
		return NULL;
	}
	for (uint64_t idx = 0; idx < structure->devices->device_count; ++idx) {
		struct cp_device_info *device =
			yanet_get_cp_device_info(structure->devices, idx);
		if (device != NULL &&
		    strcmp(device->name, STRUCTURE_TEST_DEVICE) == 0) {
			return device;
		}
	}
	return NULL;
}

// The four lists of one snapshot describe one consistent generation: the
// module with its owner, the function with its weighted chain, the
// pipeline naming that function, and the device bound to the pipeline.
static int
run_structure_snapshot_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm, 0, STRUCTURE_TEST_AGENT, STRUCTURE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_module(
			agent, dp_config, cp_config, STRUCTURE_TEST_MODULE
		),
		"failed to install the module"
	);
	TEST_ASSERT_SUCCESS(
		install_function(dp_config, cp_config, STRUCTURE_TEST_WEIGHT),
		"failed to install the function"
	);
	TEST_ASSERT_SUCCESS(
		install_pipeline(dp_config, cp_config),
		"failed to install the pipeline"
	);
	TEST_ASSERT_SUCCESS(
		install_device(agent, dp_config, cp_config),
		"failed to install the device"
	);

	struct cp_structure_info *structure =
		yanet_get_cp_structure_info(dp_config);
	TEST_ASSERT_NOT_NULL(structure, "failed to snapshot the structure");

	struct cp_module_info *module = snapshot_module(structure);
	TEST_ASSERT_NOT_NULL(
		module, "the snapshot must hold exactly one module"
	);
	TEST_ASSERT(
		strcmp(module->type, "decap") == 0,
		"the module type must survive the snapshot"
	);
	TEST_ASSERT(
		strcmp(module->name, STRUCTURE_TEST_MODULE) == 0,
		"the module name must survive the snapshot"
	);
	TEST_ASSERT(
		strcmp(module->agent, STRUCTURE_TEST_AGENT) == 0,
		"the module must carry its publishing agent"
	);

	TEST_ASSERT(
		structure->functions != NULL &&
			structure->functions->function_count == 1,
		"the snapshot must hold exactly one function"
	);
	struct cp_function_info *function =
		yanet_get_cp_function_info(structure->functions, 0);
	TEST_ASSERT(
		function != NULL &&
			strcmp(function->name, STRUCTURE_TEST_FUNCTION) == 0 &&
			function->chain_count == 1,
		"the function must survive the snapshot with its chain"
	);
	struct cp_chain_info *chain =
		yanet_get_cp_function_chain_info(function, 0);
	TEST_ASSERT(
		chain != NULL && chain->length == 0 &&
			chain->weight == STRUCTURE_TEST_WEIGHT,
		"the chain must keep its weight in the snapshot"
	);

	TEST_ASSERT(
		structure->pipelines != NULL &&
			structure->pipelines->count == 1,
		"the snapshot must hold exactly one pipeline"
	);
	struct cp_pipeline_info *pipeline =
		yanet_get_cp_pipeline_info(structure->pipelines, 0);
	struct cp_function_info_id *pipeline_function =
		yanet_get_cp_pipeline_function_info_id(pipeline, 0);
	TEST_ASSERT(
		pipeline != NULL && pipeline->length == 1 &&
			pipeline_function != NULL &&
			strcmp(pipeline_function->name,
			       STRUCTURE_TEST_FUNCTION) == 0,
		"the pipeline must keep naming its function"
	);

	TEST_ASSERT(
		structure->devices != NULL,
		"the snapshot must hold the device registry"
	);
	struct cp_device_info *device = snapshot_device(structure);
	struct cp_device_pipeline_info *input =
		yanet_get_cp_device_input_pipeline_info(device, 0);
	TEST_ASSERT(
		device != NULL && device->input_count == 1 &&
			device->output_count == 0 && input != NULL &&
			strcmp(input->name, STRUCTURE_TEST_PIPELINE) == 0 &&
			input->weight == STRUCTURE_TEST_WEIGHT,
		"the device must keep its input binding in the snapshot"
	);

	cp_structure_info_free(structure);
	agent_detach(agent);
	return TEST_SUCCESS;
}

// A module outlives the process that published it: after the publishing
// agent detaches, the snapshot still reports the module under that
// agent's name, and a later agent replacing the module moves the
// provenance to its own name.
static int
run_structure_provenance_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *earlier = agent_attach(
		shm, 0, "structure-earlier", STRUCTURE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(
		earlier,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);

	struct dp_config *dp_config = agent_dp_config(earlier);
	struct cp_config *cp_config = ADDR_OF(&earlier->cp_config);
	TEST_ASSERT_SUCCESS(
		install_module(
			earlier, dp_config, cp_config, STRUCTURE_TEST_MODULE
		),
		"failed to install the module"
	);
	agent_detach(earlier);

	struct cp_structure_info *surviving =
		yanet_get_cp_structure_info(dp_config);
	TEST_ASSERT_NOT_NULL(surviving, "failed to snapshot the structure");
	struct cp_module_info *module = snapshot_module(surviving);
	TEST_ASSERT_NOT_NULL(
		module, "the snapshot must hold exactly one module"
	);
	TEST_ASSERT(
		strcmp(module->name, STRUCTURE_TEST_MODULE) == 0 &&
			strcmp(module->agent, "structure-earlier") == 0,
		"the module must survive its publishing agent under its name"
	);
	cp_structure_info_free(surviving);

	err = NULL;
	struct agent *later = agent_attach(
		shm, 0, "structure-later", STRUCTURE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(
		later,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);

	TEST_ASSERT_SUCCESS(
		install_module(
			later, dp_config, cp_config, STRUCTURE_TEST_MODULE
		),
		"failed to replace the module"
	);

	struct cp_structure_info *replaced =
		yanet_get_cp_structure_info(dp_config);
	TEST_ASSERT_NOT_NULL(replaced, "failed to snapshot the structure");
	module = snapshot_module(replaced);
	TEST_ASSERT_NOT_NULL(
		module, "the snapshot must hold exactly one module"
	);
	TEST_ASSERT(
		strcmp(module->name, STRUCTURE_TEST_MODULE) == 0 &&
			strcmp(module->agent, "structure-later") == 0,
		"a replaced module must carry the replacing agent's name"
	);
	cp_structure_info_free(replaced);

	agent_detach(later);
	return TEST_SUCCESS;
}

// Run each scenario in fresh shared memory to isolate leaked generations.
static int
run_structure_scenario(const char *name, int (*scenario)(struct yanet_shm *)) {
	const char *port_names[] = {"01:00.0"};
	const char *devices_to_load[] = {"plain"};
	const char *modules_to_load[] = {"decap"};
	struct dataplane_ut_config config = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = modules_to_load,
		.module_count = 1,
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
	log_enable_name("debug");

	int rc = run_structure_scenario(
		"structure_snapshot", run_structure_snapshot_test
	);
	if (rc == TEST_SUCCESS) {
		rc = run_structure_scenario(
			"structure_provenance", run_structure_provenance_test
		);
	}

	return (rc == TEST_SUCCESS) ? 0 : 1;
}
