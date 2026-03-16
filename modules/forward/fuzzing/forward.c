#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "dataplane/config/zone.h"
#include "modules/forward/api/controlplane.h"
#include "modules/forward/dataplane/config.h"
#include "modules/forward/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

// Forward module filter compilation needs more memory than the default 1 MB
// arena.
//
// ASAN redzones inflate allocations significantly, so 16 MB is needed for
// fuzzing builds.
#define FORWARD_EXTRA_ARENA_SIZE (16 << 20)

static struct fuzzing_params fuzz_params = {0};

static int
forward_test_config(struct cp_module **cp_module) {
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

	// Initialize counter registry.
	//
	// Needed by "forward_module_config_update".
	if (counter_registry_init(
		    &config->cp_module.counter_registry,
		    &config->cp_module.memory_context,
		    0
	    )) {
		goto fail;
	}

	// Initialize filters and targets to empty state.
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	// Build rules covering L2, IPv4, and IPv6 filter paths.
	struct forward_rule rules[3];
	memset(rules, 0, sizeof(rules));

	// L2-only.
	strtcpy(rules[0].target, "dev1", sizeof(rules[0].target));
	strtcpy(rules[0].counter, "cnt_l2", sizeof(rules[0].counter));
	rules[0].mode = FORWARD_MODE_OUT;

	// 127.0.0.0/24.
	strtcpy(rules[1].target, "dev1", sizeof(rules[1].target));
	strtcpy(rules[1].counter, "cnt_v4", sizeof(rules[1].counter));
	rules[1].mode = FORWARD_MODE_IN;

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
	strtcpy(rules[2].target, "dev0", sizeof(rules[2].target));
	strtcpy(rules[2].counter, "cnt_v6", sizeof(rules[2].counter));
	rules[2].mode = FORWARD_MODE_IN;

	struct net6 src6 = {
		.addr = {0xfe, 0x80, [15] = 0},
		.mask =
			{0xff,
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
			 [15] = 0},
	};
	struct net6 dst6 = {
		.addr = {0xfe, 0x80, [15] = 0},
		.mask =
			{0xff,
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
			 [15] = 0},
	};
	rules[2].src_net6s.items = &src6;
	rules[2].src_net6s.count = 1;
	rules[2].dst_net6s.items = &dst6;
	rules[2].dst_net6s.count = 1;

	int rc = forward_module_config_update(&config->cp_module, rules, 3);
	if (rc != 0) {
		goto fail;
	}

	// Set up counter storage, because "forward_handle_packets" accesses
	// counters.
	if (counter_registry_link(&config->cp_module.counter_registry, NULL)) {
		goto fail;
	}

	struct counter_storage_allocator *alloc = memory_balloc(
		&fuzz_params.mctx, sizeof(struct counter_storage_allocator)
	);
	if (alloc == NULL) {
		goto fail;
	}
	counter_storage_allocator_init(alloc, &fuzz_params.mctx, 1);

	struct counter_storage *cs = counter_storage_spawn(
		&fuzz_params.mctx,
		alloc,
		NULL,
		&config->cp_module.counter_registry
	);
	if (cs == NULL) {
		goto fail;
	}
	SET_OFFSET_OF(&fuzz_params.module_ectx.counter_storage, cs);

	// Set up "mc_index" so "module_ectx_encode_device" returns invalid
	// device, causing all matched packets to be dropped safely.
	uint64_t *mc_index =
		memory_balloc(&fuzz_params.mctx, sizeof(uint64_t) * 2);
	if (mc_index == NULL) {
		goto fail;
	}
	mc_index[0] = LPM_VALUE_INVALID;
	mc_index[1] = LPM_VALUE_INVALID;
	fuzz_params.module_ectx.mc_index_size = 2;
	SET_OFFSET_OF(&fuzz_params.module_ectx.mc_index, mc_index);

	*cp_module = (struct cp_module *)config;
	return 0;

fail:
	forward_module_config_free(&config->cp_module);
	return -EINVAL;
}

static int
fuzz_setup() {
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

	return forward_test_config(&fuzz_params.cp_module);
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
