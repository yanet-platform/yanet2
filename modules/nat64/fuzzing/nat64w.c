#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include <rte_ip.h>
#include <rte_mbuf.h>

#include "modules/nat64/api/nat64cp.h"
#include "modules/nat64/dataplane/nat64dp.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

static int
nat64_test_config(struct cp_module **cp_module) {
	struct nat64_module_config *config =
		(struct nat64_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct nat64_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"nat64_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"nat64_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;
	config->mappings.count = 0;
	config->mappings.list = NULL;
	config->prefixes.prefixes = NULL;
	config->prefixes.count = 0;
	config->mtu.ipv4 = 1450;
	config->mtu.ipv6 = 1280;

	struct memory_context *memory_context =
		&config->cp_module.memory_context;
	if (lpm_init(&config->mappings.v4_to_v6, memory_context)) {
		goto error_config;
	}
	if (lpm_init(&config->mappings.v6_to_v4, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->prefixes.v6_prefixes, memory_context)) {
		goto error_lpm_v6;
	}

	// Add prefix
	uint8_t pfx[12] = {0x20, 0x01, 0x0d, 0xb8, [11] = 0x00};
	if (nat64_module_config_add_prefix((struct cp_module *)config, pfx) <
	    0) {
		goto error_lpm_prefixes;
	}

	struct mapping_item {
		uint32_t ip4;
		uint32_t ip6[4];
	} mappings[] = {
		{
			.ip4 = RTE_BE32(0xC6336401U), // 198.51.100.1
			.ip6 = {RTE_BE32(0x20010DB8U), 0, 0, RTE_BE32(0x4U)},
		},
		{
			.ip4 = RTE_BE32(0xC6336402U), // 198.51.100.2
			.ip6 = {RTE_BE32(0x20010DB8U), 0, 0, RTE_BE32(0x3U)},
		},
		{
			.ip4 = RTE_BE32(0xC6336403U), // 198.51.100.3
			.ip6 = {RTE_BE32(0x20010DB8U), 0, 0, RTE_BE32(0x2U)},
		},
		{
			.ip4 = RTE_BE32(0xC6336404U), // 198.51.100.4
			.ip6 = {RTE_BE32(0x20010DB8U), 0, 0, RTE_BE32(0x1U)},
		},
	};

	// Add mappings
	for (uint32_t i = 0; i < 4; i++) {
		if (nat64_module_config_add_mapping(
			    (struct cp_module *)config,
			    mappings[i].ip4,
			    (uint8_t *)mappings[i].ip6,
			    0
		    ) < 0) {
			goto error_mappings;
		}
	}

	*cp_module = (struct cp_module *)config;
	return 0;

error_mappings:
	if (config->mappings.list)
		memory_bfree(
			&config->cp_module.memory_context,
			config->mappings.list,
			sizeof(struct ip4to6) * config->mappings.count
		);
	if (config->prefixes.prefixes)
		memory_bfree(
			&config->cp_module.memory_context,
			config->prefixes.prefixes,
			sizeof(struct nat64_prefix) * config->prefixes.count
		);

error_lpm_prefixes:
	lpm_free(&config->prefixes.v6_prefixes);

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
	if (fuzzing_params_init(
		    &fuzz_params, "nat64 fuzzing", new_module_nat64
	    ) != 0) {
		return EXIT_FAILURE;
	}

	return nat64_test_config(&fuzz_params.cp_module);
}

RTE_LOG_REGISTER_DEFAULT(nat64test_logtype, EMERG);
#define RTE_LOGTYPE_NAT64_TEST nat64test_logtype

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		// Disable all logging during fuzzing to avoid spam
		rte_log_set_global_level(RTE_LOG_EMERG);
		rte_log_set_level(RTE_LOGTYPE_NAT64_TEST, RTE_LOG_EMERG);

		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	return fuzzing_process_packet(&fuzz_params, data, size);
}
