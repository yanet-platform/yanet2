#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "lib/fuzzing/fuzzing.h"
#include "modules/decap/api/controlplane.h"
#include "modules/decap/dataplane/config.h"
#include "modules/decap/dataplane/dataplane.h"

static struct fuzzing_params fuzz_params = {0};

static int
decap_test_config(struct cp_module **cp_module) {
	struct decap_module_config *config =
		(struct decap_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct decap_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"decap_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"decap_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;

	struct memory_context *memory_context =
		&config->cp_module.memory_context;
	if (lpm_init(&config->prefixes4, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->prefixes6, memory_context)) {
		goto error_lpm_v6;
	}

	// 127.0.0.0/24
	decap_module_config_add_prefix_v4(
		&config->cp_module,
		(uint8_t[4]){127, 0, 0, 0},
		(uint8_t[4]){127, 0, 0, 0xff}
	);
	// fe80::0/96
	decap_module_config_add_prefix_v6(
		&config->cp_module,
		(uint8_t[16]){0xfe, 0x80, [15] = 0},
		(uint8_t[16]
		){0xfe, 0x80, [12] = 0xff, [13] = 0xff, [14] = 0xff, [15] = 0xff
		}
	);

	*cp_module = (struct cp_module *)config;
	return 0;

error_lpm_v6:
	lpm_free(&config->prefixes4);

error_lpm_v4:
	memory_bfree(
		&fuzz_params.mctx, config, sizeof(struct decap_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup(void) {
	// Initialize fuzzing parameters with module loader
	if (fuzzing_params_init(&fuzz_params, "decap_fuzz", new_module_decap) !=
	    0) {
		return -1;
	}

	// Initialize module configuration
	return decap_test_config(&fuzz_params.cp_module);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	// Process packet through fuzzing infrastructure
	fuzzing_process_packet(&fuzz_params, data, size);

	return 0;
}
