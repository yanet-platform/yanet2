#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "common/memory_address.h"

#include "modules/route/api/controlplane.h"
#include "modules/route/api/fib.h"
#include "modules/route/dataplane/config.h"
#include "modules/route/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

// The table object the config links, wired to the module through the
// execution context the way a published generation carries it.
static struct route_fib_object fuzz_fib;
static struct object_ectx fuzz_object_ectx;
static struct module_object_link_ectx fuzz_object_link;

static int
route_test_fib(yanet_error **err) {
	memset(&fuzz_fib, 0, sizeof(fuzz_fib));
	memory_context_init_from(
		&fuzz_fib.cp_object.memory_context,
		&fuzz_params.mctx,
		"route_test_fib"
	);
	if (counter_registry_init(
		    &fuzz_fib.cp_object.link_counter_registry,
		    &fuzz_fib.cp_object.memory_context,
		    0
	    )) {
		return -1;
	}
	if (route_fib_init(&fuzz_fib.fib, &fuzz_fib.cp_object.memory_context)) {
		return -1;
	}

	uint64_t counter_id = counter_registry_register(
		&fuzz_fib.cp_object.link_counter_registry, "nexthop", 2, err
	);
	if (counter_id == COUNTER_INVALID) {
		goto error_table;
	}

	int route_idx = route_fib_add_route(
		&fuzz_fib.fib,
		&fuzz_fib.cp_object.memory_context,
		(struct ether_addr){
			.addr = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		},
		(struct ether_addr){
			.addr = {0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c},
		},
		0,
		counter_id
	);
	if (route_idx == -1) {
		goto error_table;
	}

	int route_list_idx = route_fib_add_route_list(
		&fuzz_fib.fib,
		&fuzz_fib.cp_object.memory_context,
		1,
		(uint32_t[]){route_idx}
	);
	if (route_list_idx == -1) {
		goto error_table;
	}

	// 127.0.0.0/24
	int rc = route_fib_add_prefix_v4(
		&fuzz_fib.fib,
		(uint8_t[4]){127, 0, 0, 0},
		(uint8_t[4]){127, 0, 0, 0xff},
		route_list_idx
	);
	if (rc != 0) {
		goto error_table;
	}

	// fe80::0/96
	rc = route_fib_add_prefix_v6(
		&fuzz_fib.fib,
		(uint8_t[16]){0xfe, 0x80, [15] = 0},
		(uint8_t[16]
		){0xfe, 0x80, [12] = 0xff, [13] = 0xff, [14] = 0xff, [15] = 0xff
		},
		route_list_idx
	);
	if (rc != 0) {
		goto error_table;
	}

	// The handler resolves the link's counter storage on every batch, so
	// the link needs one spawned from the object's registry.
	if (counter_registry_link(
		    &fuzz_fib.cp_object.link_counter_registry, NULL, err
	    )) {
		goto error_table;
	}
	struct counter_storage *link_storage = counter_storage_spawn(
		&fuzz_params.mctx,
		NULL,
		&fuzz_fib.cp_object.link_counter_registry
	);
	if (link_storage == NULL) {
		goto error_table;
	}

	memset(&fuzz_object_ectx, 0, sizeof(fuzz_object_ectx));
	memset(&fuzz_object_link, 0, sizeof(fuzz_object_link));
	SET_OFFSET_OF(&fuzz_object_ectx.cp_object, &fuzz_fib.cp_object);
	fuzz_object_ectx.abs_cp_object = &fuzz_fib.cp_object;
	SET_OFFSET_OF(&fuzz_object_link.object_ectx, &fuzz_object_ectx);
	fuzz_object_link.abs_object_ectx = &fuzz_object_ectx;
	SET_OFFSET_OF(&fuzz_object_link.counter_storage, link_storage);
	fuzz_params.module_ectx.object_link_count = 1;
	SET_OFFSET_OF(&fuzz_params.module_ectx.object_links, &fuzz_object_link);
	fuzz_params.module_ectx.abs_object_links = &fuzz_object_link;

	return 0;

error_table:
	route_fib_fini(&fuzz_fib.fib, &fuzz_fib.cp_object.memory_context);
	return -1;
}

static int
route_test_config(struct cp_module **cp_module, yanet_error **err) {
	struct route_module_config *config =
		(struct route_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct route_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"route_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"route_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;
	config->cp_module.device_count = 0;
	SET_OFFSET_OF(&config->cp_module.devices, NULL);

	// The table sits behind the only object link of the context.
	config->fib_link_idx = 0;

	// Needed by route_module_config_register_counters.
	if (counter_registry_init(
		    &config->cp_module.counter_registry,
		    &config->cp_module.memory_context,
		    0
	    )) {
		goto error_config;
	}

	// Set up counter storage, because route_handle_packets accesses
	// counters on every outcome.
	if (route_module_config_register_counters(config, err)) {
		goto error_config;
	}

	if (counter_registry_link(
		    &config->cp_module.counter_registry, NULL, err
	    )) {
		goto error_config;
	}

	struct counter_storage *cs = counter_storage_spawn(
		&fuzz_params.mctx, NULL, &config->cp_module.counter_registry
	);
	if (cs == NULL) {
		goto error_config;
	}
	SET_OFFSET_OF(&fuzz_params.module_ectx.counter_storage, cs);
	fuzz_params.module_ectx.abs_counter_storage = cs;

	*cp_module = (struct cp_module *)config;
	return 0;

error_config:
	memory_bfree(
		&fuzz_params.mctx, config, sizeof(struct route_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup(yanet_error **err) {
	if (fuzzing_params_init(
		    &fuzz_params, "route fuzzing", new_module_route
	    ) != 0) {
		return EXIT_FAILURE;
	}

	if (route_test_fib(err) != 0) {
		return EXIT_FAILURE;
	}

	if (route_test_config(&fuzz_params.cp_module, err) != 0) {
		return EXIT_FAILURE;
	}

	// Configure module_ectx for route module
	// Set up mc_index and config_gen_ectx stubs
	// TODO: For more comprehensive fuzzing, we should:
	// - Provide real device contexts instead of stubs (device_count > 0)
	// - Test with multiple mc_index values to cover different routing paths
	// - Vary config_gen_ectx to test different device configurations
	// This would allow packets to actually be routed instead of always
	// being dropped
	fuzz_params.module_ectx.mc_index_size = 1;
	SET_OFFSET_OF(
		&fuzz_params.module_ectx.mc_index, &fuzz_params.mc_index_stub
	);
	fuzz_params.module_ectx.abs_mc_index = &fuzz_params.mc_index_stub;
	SET_OFFSET_OF(
		&fuzz_params.module_ectx.config_gen_ectx,
		&fuzz_params.config_gen_ectx_stub
	);
	fuzz_params.module_ectx.abs_config_gen_ectx =
		&fuzz_params.config_gen_ectx_stub;

	return 0;
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		yanet_error *err = NULL;
		if (fuzz_setup(&err) != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	return fuzzing_process_packet(&fuzz_params, data, size);
}
