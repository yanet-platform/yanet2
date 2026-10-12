#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "modules/forward/api/controlplane.h"
#include "modules/forward/dataplane/config.h"
#include "modules/forward/dataplane/dataplane.h"

#include "objects/classifier/api/classifier_object.h"

#include "lib/classify/classifiers/device.h"
#include "lib/fuzzing/fuzzing.h"

// Forward module filter compilation needs more memory than the default 1 MB
// arena.
//
// ASAN redzones inflate allocations significantly, so 16 MB is needed for
// fuzzing builds.
#define FORWARD_EXTRA_ARENA_SIZE (16 << 20)

static struct fuzzing_params fuzz_params = {0};

// The hand-built classifier object binding: the compiled bundle and the
// prepared buffer the module's execution context carries. Static, so the
// addresses stay valid for every run of the harness.
static struct classifier_object fuzz_classifier = {0};
static struct classifier_prepared fuzz_classifier_prepared = {0};
static struct forward_prepared fuzz_forward_prepared = {0};

static int
forward_test_config(struct cp_module **cp_module, yanet_error **err) {
	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct forward_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	memset(config, 0, sizeof(struct forward_module_config));

	// Initialize cp_module fields.
	strtcpy(config->cp_module.name,
		"forward_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"forward_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;
	config->classifier_link_idx = FORWARD_CLASSIFIER_LINK_NONE;

	// Initialize counter registry.
	//
	// Needed by the target counters.
	if (counter_registry_init(
		    &config->cp_module.counter_registry,
		    &config->cp_module.memory_context,
		    0
	    )) {
		goto fail;
	}

	// Initialize targets to empty state.
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	// Build the classifier object by hand: no agent owns it, so the
	// embedded cp_object gets only the memory context the compile
	// allocates through, and the prepared handles are selected directly
	// instead of by an execution-context commit.
	//
	// The rules cover L2, IPv4, and IPv6 filter paths, the same
	// ruleset the module compiled in-house before the classification
	// moved into the object.
	struct classifier_object_rule rules[3];
	memset(rules, 0, sizeof(rules));

	// L2-only catch-all.

	// 127.0.0.0/24.
	struct net4 src4 = {
		.addr = {127, 0, 0, 0},
		.mask = {255, 255, 255, 0},
	};
	struct net4 dst4 = {
		.addr = {127, 0, 0, 0},
		.mask = {255, 255, 255, 0},
	};
	rules[1].src_net4s.items = &src4;
	rules[1].src_net4s.count = 1;
	rules[1].dst_net4s.items = &dst4;
	rules[1].dst_net4s.count = 1;

	// fe80::/96.
	struct net6 src6 = {
		.addr = {0xfe, 0x80, [15] = 0},
		.mask = {
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			[15] = 0
		},
	};
	struct net6 dst6 = {
		.addr = {0xfe, 0x80, [15] = 0},
		.mask = {
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			0xff,
			[15] = 0
		},
	};
	rules[2].src_net6s.items = &src6;
	rules[2].src_net6s.count = 1;
	rules[2].dst_net6s.items = &dst6;
	rules[2].dst_net6s.count = 1;

	memory_context_init_from(
		&fuzz_classifier.cp_object.memory_context,
		&fuzz_params.mctx,
		"classifier_fuzz"
	);
	if (classifier_object_compile(
		    &fuzz_classifier,
		    CLASSIFIER_OBJECT_SUBTYPE_L3,
		    rules,
		    3,
		    err
	    )) {
		goto fail;
	}
	classifier_object_prepare(&fuzz_classifier_prepared, &fuzz_classifier);

	// The decisions of the three rules, in the shape the module config
	// compiles them from the authored decisions.
	struct forward_target *targets = (struct forward_target *)memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct forward_target) * 3
	);
	if (targets == NULL) {
		goto fail;
	}
	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = 3;

	static const char *const counters[3] = {"cnt_l2", "cnt_v4", "cnt_v6"};
	static const uint8_t modes[3] = {
		FORWARD_MODE_OUT, FORWARD_MODE_IN, FORWARD_MODE_IN
	};

	for (uint32_t idx = 0; idx < 3; ++idx) {
		targets[idx].device_id = idx;
		targets[idx].mode = modes[idx];
		if ((targets[idx].counter_id = counter_registry_register(
			     &config->cp_module.counter_registry,
			     counters[idx],
			     2,
			     err
		     )) == (uint64_t)-1) {
			goto fail;
		}
	}

	// Set up counter storage, because "forward_handle_packets" accesses
	// counters.
	if (counter_registry_link(
		    &config->cp_module.counter_registry, NULL, err
	    )) {
		goto fail;
	}

	struct counter_storage *cs = counter_storage_spawn(
		&fuzz_params.mctx, NULL, &config->cp_module.counter_registry
	);
	if (cs == NULL) {
		goto fail;
	}
	SET_OFFSET_OF(&fuzz_params.module_ectx.counter_storage, cs);
	fuzz_params.module_ectx.abs_counter_storage = cs;

	// The classification binding resolves through the context: the
	// hand-built module context never runs the execution-context
	// commit, so the prepared handles are attached directly - the
	// zeroed device class classifies every packet as the any device,
	// and the empty device target table drops every routed packet
	// safely.
	fuzz_forward_prepared.classifier = &fuzz_classifier_prepared;
	fuzz_forward_prepared.device_class = 0;
	SET_OFFSET_OF(
		&fuzz_params.module_ectx.module_prepared, &fuzz_forward_prepared
	);
	fuzz_params.module_ectx.abs_module_prepared = &fuzz_forward_prepared;

	*cp_module = (struct cp_module *)config;
	return 0;

fail:
	// Frees directly against the harness arena instead of going through
	// the public free.
	//
	// This module configuration was built by hand with no owning agent
	// and never took the reference construction normally holds, so the
	// public free's release path would decrement a reference count that
	// starts at zero and silently wrap instead of freeing anything.
	classifier_object_fini_data(&fuzz_classifier);
	cp_module_fini(&config->cp_module);
	memory_bfree(
		&fuzz_params.mctx, config, sizeof(struct forward_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup(yanet_error **err) {
	if (fuzzing_params_init(
		    &fuzz_params, "forward fuzzing", new_module_forward
	    ) != 0) {
		return EXIT_FAILURE;
	}

	// Add extra memory arena for filter compilation.
	void *arena = malloc(FORWARD_EXTRA_ARENA_SIZE);
	if (arena == NULL) {
		return EXIT_FAILURE;
	}
	block_allocator_put_arena(
		&fuzz_params.ba, arena, FORWARD_EXTRA_ARENA_SIZE
	);

	// Create a minimal "dp_worker" for counter access.
	fuzz_params.worker =
		memory_balloc(&fuzz_params.mctx, sizeof(struct dp_worker));
	if (fuzz_params.worker == NULL) {
		return EXIT_FAILURE;
	}
	memset(fuzz_params.worker, 0, sizeof(struct dp_worker));

	return forward_test_config(&fuzz_params.cp_module, err);
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
