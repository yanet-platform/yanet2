#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include <rte_ip.h>
#include <rte_mbuf.h>

#include "dataplane/module/module.h"
#include "dataplane/module/testing.h"
#include "dataplane/packet/packet.h"
#include "modules/nat64/api/nat64cp.h"
#include "modules/nat64/dataplane/nat64dp.h"

#define ARENA_SIZE (1 << 20)
#define MBUF_MAX_SIZE 8196

struct nat64_fuzzing_params {
	struct module *module; /**< Pointer to the module being tested */
	struct module_data *module_data; /**< Module configuration */

	void *arena;
	void *payload_arena;
	struct block_allocator ba;
	struct memory_context mctx;
};

static struct nat64_fuzzing_params fuzz_params = {
	.module_data = NULL,
};

static int
nat64_test_config(struct module_data **module_data) {
	struct nat64_module_config *config =
		(struct nat64_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct nat64_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize module_data fields
	strtcpy(config->module_data.name,
		"nat64_test",
		sizeof(config->module_data.name));
	memory_context_init_from(
		&config->module_data.memory_context,
		&fuzz_params.mctx,
		"nat64_test"
	);

	config->module_data.index = 0;
	config->module_data.agent = NULL;
	config->mappings.count = 0;
	config->mappings.list = NULL;
	config->prefixes.prefixes = NULL;
	config->prefixes.count = 0;
	config->mtu.ipv4 = 1450;
	config->mtu.ipv6 = 1280;

	struct memory_context *memory_context =
		&config->module_data.memory_context;
	if (lpm_init(&config->mappings.v4_to_v6, memory_context)) {
		goto error_config;
	}
	if (lpm_init(&config->mappings.v6_to_v4, memory_context)) {
		goto error_lpm_v4;
	}

	// Add prefix
	uint8_t pfx[12] = {0x20, 0x01, 0x0d, 0xb8, [11] = 0x00};
	if (nat64_module_config_add_prefix((struct module_data *)config, pfx) <
	    0) {
		goto error_lpm_v6;
	}

	struct mapping_item {
		uint32_t ip4;
		uint32_t ip6[4];
	} mappings[] = {
		{
			.ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 1)),
			.ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x4)},
		},
		{
			.ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 2)),
			.ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x3)},
		},
		{
			.ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 3)),
			.ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x2)},
		},
		{
			.ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 4)),
			.ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x1)},
		},
	};

	// Add mappings
	for (uint32_t i = 0; i < 4; i++) {
		if (nat64_module_config_add_mapping(
			    (struct module_data *)config,
			    mappings[i].ip4,
			    (uint8_t *)mappings[i].ip6,
			    0
		    ) < 0) {
			goto error_mappings;
		}
	}

	*module_data = (struct module_data *)config;
	return 0;

error_mappings:
	if (config->mappings.list)
		memory_bfree(
			&config->module_data.memory_context,
			config->mappings.list,
			sizeof(struct ip4to6) * config->mappings.count
		);
	if (config->prefixes.prefixes)
		memory_bfree(
			&config->module_data.memory_context,
			config->prefixes.prefixes,
			sizeof(struct nat64_prefix) * config->prefixes.count
		);

error_lpm_v6:
	lpm_free(&config->mappings.v6_to_v4);

error_lpm_v4:
	lpm_free(&config->mappings.v4_to_v6);

error_config:
	if (config) {
		memory_bfree(
			&fuzz_params.mctx,
			config,
			sizeof(struct nat64_module_config)
		);
	}
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
		&fuzz_params.mctx, "nat64 fuzzing", &fuzz_params.ba
	);

	fuzz_params.module = new_module_nat64();
	fuzz_params.payload_arena = memory_balloc(
		&fuzz_params.mctx,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4
	);
	if (fuzz_params.payload_arena == NULL) {
		return -ENOMEM;
	}

	return nat64_test_config(&fuzz_params.module_data);
}
RTE_LOG_REGISTER_DEFAULT(nat64test_logtype, EMERG);
#define RTE_LOGTYPE_NAT64_TEST nat64test_logtype

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		rte_log_set_level(RTE_LOGTYPE_NAT64_TEST, RTE_LOG_EMERG);
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
	// Process packet through NAT64 module
	fuzz_params.module->handler(NULL, fuzz_params.module_data, pf);

	return 0;
}
