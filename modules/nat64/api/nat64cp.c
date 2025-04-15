/* System headers */
#include <errno.h>
#include <inttypes.h>
#include <string.h>

/* Project headers */
#include "common.h"
#include "config.h"
#include "nat64cp.h"

/* Common headers */
#include "common/container_of.h"
#include "common/exp_array.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "logging/log.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct cp_module *
nat64_module_config_create(struct agent *agent, const char *name) {
	struct nat64_module_config *config =
		(struct nat64_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct nat64_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "nat64",
		    name,
		    nat64_module_config_free
	    )) {
		goto error_init;
	}

	if (nat64_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		goto error_init;
	}

	return &config->cp_module;

error_init:
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct nat64_module_config)
	);
	errno = ENOMEM;
	return NULL;
}

void
nat64_module_config_free(struct cp_module *cp_module) {
	LOG(DEBUG, "Starting cleanup of NAT64 module '%s'", cp_module->name);

	struct nat64_module_config *config =
		container_of(cp_module, struct nat64_module_config, cp_module);

	nat64_module_config_data_destroy(
		config, &config->cp_module.memory_context
	);

	// Free main config structure
	struct agent *agent = ADDR_OF(&cp_module->agent);
	if (cp_module->agent) {
		LOG(DEBUG,
		    "Freeing main config structure: size=%zu bytes, address=%p",
		    sizeof(struct nat64_module_config),
		    (void *)config);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct nat64_module_config)
		);
	}

	LOG(DEBUG, "Completed cleanup of NAT64 module '%s'", cp_module->name);
}

int
nat64_module_config_data_init(
	struct nat64_module_config *config,
	struct memory_context *memory_context
) {
	// Initialize LPM structures
	if (lpm_init(&config->mappings.v4_to_v6, memory_context)) {
		LOG(ERROR, "Failed to initialize v4_to_v6 LPM");
		goto error_lpm_v4;
	}
	if (lpm_init(&config->mappings.v6_to_v4, memory_context)) {
		LOG(ERROR, "Failed to initialize v6_to_v4 LPM");
		goto error_lpm_v6;
	}

	// Initialize v6 prefixes LPM
	if (lpm_init(&config->prefixes.v6_prefixes, memory_context)) {
		LOG(ERROR, "Failed to initialize v6_prefixes LPM");
		goto error_lpm_prefixes;
	}

	// Initialize other fields
	config->mappings.count = 0;
	config->mappings.list = NULL;
	config->prefixes.prefixes = NULL;
	config->prefixes.count = 0;
	config->mtu.ipv6 = 1280; // Minimum IPv6 MTU
	config->mtu.ipv4 = 1450; // Default IPv4 MTU

	config->mappings.drop_unknown_mapping = false;
	config->prefixes.drop_unknown_prefix = false;

	return 0;

error_lpm_prefixes:
	lpm_free(&config->mappings.v6_to_v4);
error_lpm_v6:
	lpm_free(&config->mappings.v4_to_v6);
error_lpm_v4:

	return -1;
}

void
nat64_module_config_data_destroy(
	struct nat64_module_config *config,
	struct memory_context *memory_context
) {
	LOG(DEBUG,
	    "Freeing v4_to_v6 LPM table at %p",
	    (void *)&config->mappings.v4_to_v6);
	lpm_free(&config->mappings.v4_to_v6);

	LOG(DEBUG,
	    "Freeing v6_to_v4 LPM table at %p",
	    (void *)&config->mappings.v6_to_v4);
	lpm_free(&config->mappings.v6_to_v4);

	LOG(DEBUG,
	    "Freeing v6_prefixes LPM table at %p",
	    (void *)&config->prefixes.v6_prefixes);
	lpm_free(&config->prefixes.v6_prefixes);

	if (config->mappings.list) {
		struct ip4to6 *mapping_list = ADDR_OF(&config->mappings.list);
		size_t mappings_size =
			sizeof(struct ip4to6) * config->mappings.count;
		LOG(DEBUG,
		    "Freeing mappings list: count=%zu, size=%zu bytes, "
		    "address=%p",
		    config->mappings.count,
		    mappings_size,
		    (void *)mapping_list);

		memory_bfree(memory_context, mapping_list, mappings_size);
	} else {
		LOG(DEBUG, "No mappings list to free");
	}

	if (config->prefixes.prefixes) {
		size_t prefixes_size =
			sizeof(struct nat64_prefix) * config->prefixes.count;
		struct nat64_prefix *prefixes =
			ADDR_OF(&config->prefixes.prefixes);
		LOG(DEBUG,
		    "Freeing prefixes array: count=%zu, size=%zu bytes, "
		    "address=%p",
		    config->prefixes.count,
		    prefixes_size,
		    (void *)prefixes);

		memory_bfree(memory_context, prefixes, prefixes_size);
	} else {
		LOG(DEBUG, "No prefixes array to free");
	}
}

int
nat64_module_config_add_mapping(
	struct cp_module *cp_module,
	uint32_t ip4,
	uint8_t ip6[16],
	size_t prefix_num
) {
	struct nat64_module_config *config =
		container_of(cp_module, struct nat64_module_config, cp_module);

	// Validate prefix index
	if (prefix_num >= config->prefixes.count) {
		LOG(ERROR,
		    "Invalid prefix index %zu (max %zu)",
		    prefix_num,
		    config->prefixes.count);
		errno = EINVAL;
		return -1;
	}

	// Expand mapping array
	struct ip4to6 *mappings = ADDR_OF(&config->mappings.list);
	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&mappings,
		    sizeof(*mappings),
		    &config->mappings.count
	    )) {
		LOG(ERROR, "Failed to expand mapping array");
		errno = ENOMEM;
		return -1;
	}

	// Add new mapping
	mappings[config->mappings.count - 1] =
		(struct ip4to6){.ip4 = ip4, .prefix_index = prefix_num};
	memcpy(mappings[config->mappings.count - 1].ip6, ip6, 16);
	SET_OFFSET_OF(&config->mappings.list, mappings);

	// Add to LPM structures
	// First try to insert into v6_to_v4
	if (lpm_insert(
		    &config->mappings.v6_to_v4,
		    16,
		    ip6,
		    ip6,
		    config->mappings.count - 1
	    )) {
		LOG(ERROR, "Failed to insert mapping into v6_to_v4 LPM");
		errno = ENOMEM;
		return -1;
	}

	// Then insert into v4_to_v6
	if (lpm_insert(
		    &config->mappings.v4_to_v6,
		    4,
		    (uint8_t *)&ip4,
		    (uint8_t *)&ip4,
		    config->mappings.count - 1
	    )) {
		LOG(ERROR, "Failed to insert mapping into v4_to_v6 LPM");
		errno = ENOMEM;
		return -1;
	}

	LOG(DEBUG,
	    "Added mapping IPv4 -> IPv6: " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT,
	    IPv4_BYTES_LE(ip4),
	    IPv6_BYTES(ip6));

	return config->mappings.count - 1;
}

int
nat64_module_config_add_prefix(
	struct cp_module *cp_module, uint8_t prefix[12]
) {
	struct nat64_module_config *config =
		container_of(cp_module, struct nat64_module_config, cp_module);

	// Expand prefix array
	struct nat64_prefix *prefixes = ADDR_OF(&config->prefixes.prefixes);
	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&prefixes,
		    sizeof(*prefixes),
		    &config->prefixes.count
	    )) {
		LOG(ERROR, "Failed to expand prefix array");
		errno = ENOMEM;
		return -1;
	}

	// Add new prefix
	prefixes[config->prefixes.count - 1] = (struct nat64_prefix){};
	memcpy(prefixes[config->prefixes.count - 1].prefix, prefix, 12);
	SET_OFFSET_OF(&config->prefixes.prefixes, prefixes);

	// Add to LPM structure
	if (lpm_insert(
		    &config->prefixes.v6_prefixes,
		    12,
		    prefix,
		    prefix,
		    config->prefixes.count - 1
	    )) {
		LOG(ERROR, "Failed to insert prefix into v6_prefixes LPM");
		errno = ENOMEM;
		return -1;
	}

	LOG(DEBUG,
	    "Added prefix "
	    "%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x\n",
	    prefix[0],
	    prefix[1],
	    prefix[2],
	    prefix[3],
	    prefix[4],
	    prefix[5],
	    prefix[6],
	    prefix[7],
	    prefix[8],
	    prefix[9],
	    prefix[10],
	    prefix[11]);

	return config->prefixes.count - 1;
}

int
nat64_module_config_set_drop_unknown(
	struct cp_module *cp_module,
	bool drop_unknown_prefix,
	bool drop_unknown_mapping
) {
	if (!cp_module) {
		errno = EINVAL;
		return -1;
	}

	struct nat64_module_config *config =
		container_of(cp_module, struct nat64_module_config, cp_module);

	config->prefixes.drop_unknown_prefix = drop_unknown_prefix;
	config->mappings.drop_unknown_mapping = drop_unknown_mapping;

	LOG(DEBUG,
	    "Set drop unknown flags: prefix=%d, mapping=%d",
	    drop_unknown_prefix,
	    drop_unknown_mapping);

	return 0;
}
