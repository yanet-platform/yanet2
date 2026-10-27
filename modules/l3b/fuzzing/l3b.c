/*
 * Fuzz target for the l3b balancer module over the shared packet
 * helper.
 *
 * The module configuration is allocated by hand from the fuzzing arena,
 * in the zero-rule state of an installed module before its first
 * update: the destination classifiers stay zeroed and are never
 * queried, and the zeroed execution context resolves no linked virtual
 * service. Parsed inputs therefore exercise the module's own guards —
 * fragment exclusion, transport eligibility, head residency, the
 * unmatched-transport drop and the unmatched-Echo pass-through — while
 * destination classification and service dispatch, which need a
 * published generation carrying rules and a linked service, stay out
 * of this target's reach.
 */

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "common/strutils.h"

#include "modules/l3b/dataplane/config.h"
#include "modules/l3b/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

static int
l3b_test_config(struct cp_module **cp_module) {
	struct module_config *config = (struct module_config *)memory_balloc(
		&fuzz_params.mctx, sizeof(struct module_config)
	);
	if (config == NULL) {
		return -ENOMEM;
	}

	// The arena does not zero, and every unset field must read as
	// empty: no rules, no links, zeroed classifiers.
	memset(config, 0, sizeof(*config));
	strtcpy(config->cp_module.name,
		"l3b_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.entity.memory_context,
		&fuzz_params.mctx,
		"l3b_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.entity.agent = NULL;

	*cp_module = (struct cp_module *)config;
	return 0;
}

static int
fuzz_setup() {
	if (fuzzing_params_init(&fuzz_params, "l3b fuzzing", new_module_l3b) !=
	    0) {
		return EXIT_FAILURE;
	}

	return l3b_test_config(&fuzz_params.cp_module);
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
