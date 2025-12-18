#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "common/memory_address.h"
#include "modules/route/api/controlplane.h"
#include "modules/route/dataplane/config.h"
#include "modules/route/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

static int
route_test_config(struct cp_module **cp_module) {
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

	struct memory_context *memory_context =
		&config->cp_module.memory_context;
	if (lpm_init(&config->lpm_v4, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->lpm_v6, memory_context)) {
		goto error_lpm_v6;
	}
	config->route_count = 0;
	config->routes = NULL;

	config->route_list_count = 0;
	config->route_lists = NULL;

	config->route_index_count = 0;
	config->route_indexes = NULL;

	struct cp_module *rmc = &config->cp_module;

	int route_idx = route_module_config_add_route(
		rmc,
		(struct ether_addr){
			.addr = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		},
		(struct ether_addr){
			.addr = {0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c},
		},
		"dev0"
	);
	if (route_idx == -1) {
		goto error_lpm_v6;
	}

	int route_list_idx = route_module_config_add_route_list(
		rmc, 1, (uint32_t[]){route_idx}
	);
	if (route_list_idx == -1) {
		goto error_lpm_v6;
	}

	// 127.0.0.0/24
	int rc = route_module_config_add_prefix_v4(
		rmc,
		(uint8_t[4]){127, 0, 0, 0},
		(uint8_t[4]){127, 0, 0, 0xff},
		route_list_idx
	);
	if (rc != 0) {
		goto error_lpm_v6;
	}
	// fe80::0/96
	rc = route_module_config_add_prefix_v6(
		rmc,
		(uint8_t[16]){0xfe, 0x80, [15] = 0},
		(uint8_t[16]
		){0xfe, 0x80, [12] = 0xff, [13] = 0xff, [14] = 0xff, [15] = 0xff
		},
		route_list_idx
	);
	if (rc != 0) {
		goto error_lpm_v6;
	}

	*cp_module = (struct cp_module *)config;
	return 0;

error_lpm_v6:
	lpm_free(&config->lpm_v4);

error_lpm_v4:
	memory_bfree(
		&fuzz_params.mctx, config, sizeof(struct route_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup() {
	if (fuzzing_params_init(
		    &fuzz_params, "route fuzzing", new_module_route
	    ) != 0) {
		return EXIT_FAILURE;
	}

	if (route_test_config(&fuzz_params.cp_module) != 0) {
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
	SET_OFFSET_OF(
		&fuzz_params.module_ectx.config_gen_ectx,
		&fuzz_params.config_gen_ectx_stub
	);

	return 0;
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	return fuzzing_process_packet(&fuzz_params, data, size);
}
