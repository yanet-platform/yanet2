#include "common/container_of.h"
#include "common/memory.h"
#include "dataplane/module/module.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/logging/log.h"

#include "test_assert.h"
#include "yanet_mock.h"
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

struct dummy_module_config {
	struct cp_module cp_module;
	char text[80];
};

static void
dummy_module_free(struct cp_module *cp_module) {
	(void)cp_module;
}

static struct cp_module *
dummy_module_config_create(struct agent *agent, char *text) {
	struct dummy_module_config *dummy =
		memory_balloc(&agent->memory_context, sizeof(*dummy));
	if (dummy == NULL) {
		return NULL;
	}
	memset(dummy->text, 0, sizeof(dummy->text));
	memcpy(dummy->text, text, strlen(text));
	int res = cp_module_init(
		&dummy->cp_module, agent, "dummy", "dummy0", dummy_module_free
	);
	if (res != 0) {
		return NULL;
	}
	return &dummy->cp_module;
}

static char result[80];

static void
handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)packet_front;
	struct dummy_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct dummy_module_config,
		cp_module
	);
	memcpy(result, config->text, sizeof(result));
}

////////////////////////////////////////////////////////////////////////////////

int
basic() {
	// create storage for mock
	void *storage = aligned_alloc(64, 1 << 28);
	TEST_ASSERT_NOT_NULL(storage, "failed to alloc storage");

	// init mock with single dummy module (in could be `balancer`, `forward`
	// or `acl` or any other)
	char *type = "dummy";
	struct yanet_mock mock;
	int res = yanet_mock_init(&mock, storage, 1 << 12, 1 << 27, &type, 1);
	TEST_ASSERT_EQUAL(res, 0, "failed to init mock");

	// attach agent
	struct agent *agent = yanet_mock_agent_attach(&mock, "agent", 1 << 12);
	TEST_ASSERT_NOT_NULL(agent, "failed to attach agent");

	// prepare for next controlplane generation
	yanet_mock_cp_update_prepare(&mock);

	// create module config
	struct cp_module *dummy =
		dummy_module_config_create(agent, "im dummy module");
	TEST_ASSERT_NOT_NULL(dummy, "failed to create dummy module");
	struct dummy_module_config *config =
		container_of(dummy, struct dummy_module_config, cp_module);
	LOG(DEBUG, "dummy text: %s", config->text);

	// insert `cp_module` into dataplane registry
	res = agent_update_modules(agent, 1, &dummy);
	TEST_ASSERT_EQUAL(res, 0, "failed to update modules");

	// call module to handle packets

	// create packet front
	struct packet_front packet_front;
	packet_front_init(&packet_front);
	// add some packets here
	// packet_list_add(&packet_front_pass.input, &packet1)
	// ...
	//

	// call to handle added packets
	yanet_mock_handle_packets(&mock, dummy, &packet_front, handle_packets);

	// check handle_packets has been called
	int cmp_result = strcmp(result, "im dummy module");
	TEST_ASSERT_EQUAL(cmp_result, 0, "bad content");

	// free storage
	free(storage);

	// success
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");
	LOG(INFO, "running test `basic` ...");
	int res = basic();
	TEST_ASSERT_EQUAL(res, TEST_SUCCESS, "test `basic` failed");
	LOG(INFO, "all tests have been passed");
	return 0;
}