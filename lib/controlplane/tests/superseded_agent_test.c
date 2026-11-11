/*
 * Updates through a superseded agent are refused.
 *
 * A same-name attach ends the previous owner's lifetime: items it would
 * still create would raise a predecessor's loaded count from zero and
 * race the sweep's reclamation, which runs under the agent lock alone.
 * The module, device and object update paths therefore refuse items
 * owned by a superseded agent before building a generation, leaving the
 * published configuration and the owner's loaded count untouched.
 */

#include "api/agent.h"

#include "common/test_assert.h"

#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/reclaim.h"
#include "lib/controlplane/config/zone.h"
#include "modules/decap/api/controlplane.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>
#include <string.h>

#define SUPERSEDED_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

static struct cp_device *
new_plain_device(struct agent *agent, const char *name, yanet_error **err) {
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(name, 0, 0, err);
	if (cfg == NULL) {
		return NULL;
	}
	struct cp_device *dev = cp_device_plain_new(agent, cfg, err);
	cp_device_plain_config_free(cfg);
	return dev;
}

static int
run_superseded_refusal_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	// Agent A installs a device; a same-name attach then supersedes A.
	struct agent *agent_a = agent_attach(
		shm, 0, "superseded", SUPERSEDED_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_a, "agent_attach failed for agent A");

	struct dp_config *dp_config = agent_dp_config(agent_a);
	struct cp_config *cp_config = ADDR_OF(&agent_a->cp_config);

	struct cp_device *dev_a = new_plain_device(agent_a, "dev0", &err);
	TEST_ASSERT_NOT_NULL(dev_a, "device new failed for agent A");
	struct cp_device *devs_a[] = {dev_a};
	TEST_ASSERT_SUCCESS(
		cp_config_update_devices(dp_config, cp_config, 1, devs_a, &err),
		"update_devices failed for agent A"
	);

	struct agent *agent_b = agent_attach(
		shm, 0, "superseded", SUPERSEDED_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_b, "agent_attach failed for agent B");
	TEST_ASSERT(
		!agent_is_current(cp_config, agent_a),
		"superseded agent A must not be the registry head"
	);
	TEST_ASSERT(
		agent_is_current(cp_config, agent_b),
		"agent B must be the registry head"
	);

	// A device still created through agent A must be refused, and the
	// refusal must leave the published generation and A's loaded count
	// untouched: no new generation was built, no reference was gained.
	struct cp_device *dev_a2 = new_plain_device(agent_a, "dev1", &err);
	TEST_ASSERT_NOT_NULL(dev_a2, "device new failed for agent A");
	struct cp_device *devs_a2[] = {dev_a2};
	struct cp_config_gen *gen_before = cp_config_current_gen(cp_config);
	uint64_t count_before = agent_a->loaded_entity_count;

	yanet_error *refuse_err = NULL;
	TEST_ASSERT(
		cp_config_update_devices(
			dp_config, cp_config, 1, devs_a2, &refuse_err
		) == -1,
		"update through a superseded agent must fail"
	);
	TEST_ASSERT_EQUAL(
		yanet_error_kind(refuse_err),
		YANET_ERROR_FAILED_PRECONDITION,
		"the refusal must carry the failed-precondition kind"
	);
	yanet_error_free(refuse_err);

	TEST_ASSERT(
		cp_config_current_gen(cp_config) == gen_before,
		"a refused update must not publish a generation"
	);
	TEST_ASSERT_EQUAL(
		agent_a->loaded_entity_count,
		count_before,
		"a refused update must not gain a reference for the superseded "
		"owner"
	);

	// A module created through agent A must be refused the same way,
	// leaving the published generation untouched.
	struct cp_module *mod_a = decap_module_config_new(agent_a, "m0", &err);
	TEST_ASSERT_NOT_NULL(mod_a, "module config new failed for agent A");
	struct cp_module *mods_a[] = {mod_a};

	refuse_err = NULL;
	TEST_ASSERT(
		cp_config_update_modules(
			dp_config, cp_config, 1, mods_a, &refuse_err
		) == -1,
		"module update through a superseded agent must fail"
	);
	TEST_ASSERT_EQUAL(
		yanet_error_kind(refuse_err),
		YANET_ERROR_FAILED_PRECONDITION,
		"the module refusal must carry the failed-precondition kind"
	);
	yanet_error_free(refuse_err);
	TEST_ASSERT(
		cp_config_current_gen(cp_config) == gen_before,
		"a refused module update must not publish a generation"
	);

	// An object created through agent A must be refused the same way.
	struct cp_object *obj_a =
		fwstate_map_v4_object_config_new(agent_a, "o0", &err);
	TEST_ASSERT_NOT_NULL(
		obj_a,
		"object config new failed for agent A: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_object *objs_a[] = {obj_a};

	refuse_err = NULL;
	TEST_ASSERT(
		cp_config_update_objects(
			dp_config, cp_config, 1, objs_a, &refuse_err
		) == -1,
		"object update through a superseded agent must fail"
	);
	TEST_ASSERT_EQUAL(
		yanet_error_kind(refuse_err),
		YANET_ERROR_FAILED_PRECONDITION,
		"the object refusal must carry the failed-precondition kind"
	);
	yanet_error_free(refuse_err);
	TEST_ASSERT(
		cp_config_current_gen(cp_config) == gen_before,
		"a refused object update must not publish a generation"
	);

	// The successor's own update still succeeds and retires the
	// generation holding agent A's device.
	struct cp_device *dev_b = new_plain_device(agent_b, "dev0", &err);
	TEST_ASSERT_NOT_NULL(dev_b, "device new failed for agent B");
	struct cp_device *devs_b[] = {dev_b};
	TEST_ASSERT_SUCCESS(
		cp_config_update_devices(dp_config, cp_config, 1, devs_b, &err),
		"update_devices failed for agent B"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};
	const char *mods_to_load[] = {"decap"};
	const char *objs_to_load[] = {FWSTATE_MAP_V4_OBJECT_TYPE};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = mods_to_load,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
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

	int res = run_superseded_refusal_test(shm);

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
