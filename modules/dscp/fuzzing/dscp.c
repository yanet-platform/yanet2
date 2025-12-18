#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "modules/dscp/api/controlplane.h"
#include "modules/dscp/dataplane/config.h"
#include "modules/dscp/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

static int
dscp_test_config(struct cp_module **cp_module) {
	struct dscp_module_config *config =
		(struct dscp_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct dscp_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"dscp_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"dscp_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;

	struct memory_context *memory_context =
		&config->cp_module.memory_context;
	if (lpm_init(&config->lpm_v4, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->lpm_v6, memory_context)) {
		goto error_lpm_v6;
	}

	// 127.0.0.0/24
	int rc = dscp_module_config_add_prefix_v4(
		&config->cp_module,
		(uint8_t[4]){127, 0, 0, 0},
		(uint8_t[4]){127, 0, 0, 0xff}
	);
	if (rc != 0) {
		goto error_lpm_v6;
	}
	// fe80::0/96
	rc = dscp_module_config_add_prefix_v6(
		&config->cp_module,
		(uint8_t[16]){0xfe, 0x80, [15] = 0},
		(uint8_t[16]
		){0xfe, 0x80, [12] = 0xff, [13] = 0xff, [14] = 0xff, [15] = 0xff
		}
	);
	if (rc != 0) {
		goto error_lpm_v6;
	}

	rc = dscp_module_config_set_dscp_marking(
		&config->cp_module, DSCP_MARK_DEFAULT, 46
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
		&fuzz_params.mctx, config, sizeof(struct dscp_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup() {
	if (fuzzing_params_init(
		    &fuzz_params, "dscp fuzzing", new_module_dscp
	    ) != 0) {
		return EXIT_FAILURE;
	}

	return dscp_test_config(&fuzz_params.cp_module);
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
