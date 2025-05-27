#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE
#include <rte_build_config.h>	// RTE_PKTMBUF_HEADROOM

#include "dataplane/module/module.h"
#include "dataplane/module/testing.h"
#include "dataplane/packet/packet.h"
#include "modules/forward/api/controlplane.h"
#include "modules/forward/dataplane/config.h"
#include "modules/forward/dataplane/dataplane.h"

#define ARENA_SIZE (1 << 20)

struct forward_fuzzing_params {
	struct module *module; /**< Pointer to the module being tested */
	struct module_data *module_data; /**< Module configuration */

	void *arena;
	void *payload_arena;
	struct block_allocator ba;
	struct memory_context mctx;
};

static struct forward_fuzzing_params fuzz_params = {
	.module_data = NULL,
};

static int
forward_test_config(struct module_data **module_data) {
	uint16_t device_count = 2;

	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&fuzz_params.mctx,
			sizeof(struct forward_module_config
			) + sizeof(struct forward_device_config) * device_count
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize module_data fields
	strtcpy(config->module_data.name,
		"forward_test",
		sizeof(config->module_data.name));
	memory_context_init_from(
		&config->module_data.memory_context,
		&fuzz_params.mctx,
		"forward_test"
	);

	config->module_data.index = 0;
	config->module_data.agent = NULL;
	config->device_count = device_count;
	config->module_data.free_handler = forward_module_config_free;

	struct memory_context *memory_context =
		&config->module_data.memory_context;

	for (uint16_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		struct forward_device_config *device_forward =
			config->device_forwards + dev_idx;
		device_forward->l2_dst_device_id = dev_idx;
		if (lpm_init(&device_forward->lpm_v4, memory_context)) {
			goto fail;
		}
		if (lpm_init(&device_forward->lpm_v6, memory_context)) {
			goto fail;
		}
	}
	for (uint16_t idx = 0; idx < device_count; idx++) {
		uint16_t from_dev = idx;
		uint16_t to_dev = device_count - idx - 1;
		int rc = forward_module_config_enable_l2(
			&config->module_data, from_dev, to_dev
		);
		if (rc != 0) {
			goto fail;
		}

		// 127.0.0.0/24
		rc = forward_module_config_enable_v4(
			&config->module_data,
			(uint8_t[4]){127, 0, 0, 0},
			(uint8_t[4]){127, 0, 0, 0xff},
			from_dev,
			to_dev
		);
		if (rc != 0) {
			goto fail;
		}
		// fe80::0/96
		rc = forward_module_config_enable_v6(
			&config->module_data,
			(uint8_t[16]){0xfe, 0x80, [15] = 0},
			(uint8_t[16]){0xfe,
				      0x80,
				      [12] = 0xff,
				      [13] = 0xff,
				      [14] = 0xff,
				      [15] = 0xff},
			from_dev,
			to_dev
		);
		if (rc != 0) {
			goto fail;
		}
	}

	*module_data = (struct module_data *)config;
	return 0;

fail:
	forward_module_config_free(&config->module_data);
	return -EINVAL;
}

static int
fuzz_setup() {
	fuzz_params.arena = malloc(ARENA_SIZE);
	if (fuzz_params.arena == NULL) {
		return EXIT_FAILURE;
	}

	block_allocator_init(&fuzz_params.ba);
	block_allocator_put_arena(
		&fuzz_params.ba, fuzz_params.arena, ARENA_SIZE
	);

	memory_context_init(
		&fuzz_params.mctx, "forward fuzzing", &fuzz_params.ba
	);

	fuzz_params.module = new_module_forward();
	fuzz_params.payload_arena = memory_balloc(
		&fuzz_params.mctx,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4
	);
	if (fuzz_params.payload_arena == NULL) {
		return -ENOMEM;
	}

	return forward_test_config(&fuzz_params.module_data);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	if (size > (MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM)) {
		return 0;
	}
	struct test_data payload[] = {{.payload = data, .size = size}};

	struct packet_front *pf = testing_packet_front(
		payload,
		fuzz_params.payload_arena,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4,
		1,
		MBUF_MAX_SIZE
	);

	parse_packet(pf->input.first);
	// Process packet through forward module
	fuzz_params.module->handler(NULL, fuzz_params.module_data, pf);

	return 0;
}
