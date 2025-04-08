/* System headers */
#include <errno.h>
#include <inttypes.h>
#include <string.h>

/* DPDK headers */
#include <rte_log.h>

/* Project headers */
#include "nat64cp.h"
#include "config.h"
#include "common.h"

/* Common headers */
#include "common/container_of.h"
#include "common/exp_array.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

/**
 * @def RTE_LOGTYPE_NAT64CP
 * @brief Define log type for NAT64CP module.
 *
 * This macro defines a specific log type for the NAT64CP module.
 * The log type is registered using RTE_LOG_REGISTER_DEFAULT with the log level
 * set to DEBUG if DEBUG_NAT64 is defined, otherwise INFO.
 *
 * @note For more details on RTE_LOG and log types, refer to the DPDK
 * documentation: https://doc.dpdk.org/guides/prog_guide/log_lib.html
 */
#ifdef DEBUG_NAT64
RTE_LOG_REGISTER_DEFAULT(nat64cp_logtype, DEBUG);
#else
RTE_LOG_REGISTER_DEFAULT(nat64cp_logtype, INFO);
#endif
#define RTE_LOGTYPE_NAT64CP nat64cp_logtype

struct module_data *
nat64_module_config_init(struct agent *agent, const char *name) {
    struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

    uint64_t index;
    if (dp_config_lookup_module(dp_config, "nat64", &index)) {
        errno = ENXIO;
        return NULL;
    }

    struct nat64_module_config *config =
        (struct nat64_module_config *)memory_balloc(
            &agent->memory_context,
            sizeof(struct nat64_module_config)
        );
    if (config == NULL) {
        errno = ENOMEM;
        return NULL;
    }

    config->module_data.index = index;
    strtcpy(config->module_data.name, name, sizeof(config->module_data.name));
    memory_context_init_from(
        &config->module_data.memory_context,
        &agent->memory_context,
        name
    );
    SET_OFFSET_OF(&config->module_data.agent, agent);
    config->module_data.free_handler = nat64_module_config_free;

    // From this point all allocations are made on local memory context
    struct memory_context *memory_context = &config->module_data.memory_context;

    // Initialize LPM structures
    if (lpm_init(&config->mappings.v4_to_v6, memory_context))
        goto error_cleanup;
    if (lpm_init(&config->mappings.v6_to_v4, memory_context))
        goto error_lpm_v6;

    // Initialize other fields
    config->mappings.count = 0;
    config->mappings.list = NULL;
    config->prefixes.prefixes = NULL;
    config->prefixes.count = 0;
    config->mtu.ipv6 = 1280;  // Minimum IPv6 MTU
    config->mtu.ipv4 = 1450;  // Default IPv4 MTU

    RTE_LOG(DEBUG, NAT64CP, "Initialized NAT64 module '%s'\n", name);
    return &config->module_data;

error_lpm_v6:
    lpm_free(&config->mappings.v4_to_v6);
    goto error_cleanup;

error_cleanup:
    memory_bfree(
        &agent->memory_context,
        config,
        sizeof(struct nat64_module_config)
    );
    errno = ENOMEM;
    return NULL;
}

void
nat64_module_config_free(struct module_data *module_data) {
    struct nat64_module_config *config = container_of(
        module_data, struct nat64_module_config, module_data
    );

    // Free LPM structures
    lpm_free(&config->mappings.v4_to_v6);
    lpm_free(&config->mappings.v6_to_v4);

    // Free arrays
    if (config->mappings.list) {
        memory_bfree(
            &module_data->memory_context,
            config->mappings.list,
            sizeof(struct ip4to6) * config->mappings.count
        );
    }
    if (config->prefixes.prefixes) {
        memory_bfree(
            &module_data->memory_context,
            config->prefixes.prefixes,
            sizeof(struct nat64_prefix) * config->prefixes.count
        );
    }

    RTE_LOG(DEBUG, NAT64CP, "Freed NAT64 module '%s'\n", module_data->name);

    // Free main config structure
    struct agent *agent = ADDR_OF(&module_data->agent);
    memory_bfree(
        &agent->memory_context,
        config,
        sizeof(struct nat64_module_config)
    );
}

int
nat64_module_config_add_mapping(
    struct module_data *module_data,
    uint32_t ip4,
    uint8_t ip6[16],
    size_t prefix_num
) {
    struct nat64_module_config *config = container_of(
        module_data, struct nat64_module_config, module_data
    );

    // Validate prefix index
    if (prefix_num >= config->prefixes.count) {
        errno = EINVAL;
        return -1;
    }

    // Expand mapping array
    struct ip4to6 *mappings = ADDR_OF(&config->mappings.list);
    if (mem_array_expand_exp(
            &config->module_data.memory_context,
            (void **)&mappings,
            sizeof(*mappings),
            &config->mappings.count
        )) {
        errno = ENOMEM;
        return -1;
    }

    // Add new mapping
    mappings[config->mappings.count - 1] = (struct ip4to6){
        .ip4 = ip4,
        .prefix_index = prefix_num
    };
    memcpy(mappings[config->mappings.count - 1].ip6, ip6, 16);
    SET_OFFSET_OF(&config->mappings.list, mappings);

    // Add to LPM structures
    // First try to insert into v6_to_v4
    if (lpm_insert(&config->mappings.v6_to_v4, 16, ip6, ip6, config->mappings.count - 1)) {
        errno = ENOMEM;
        return -1;
    }
    
    // Then insert into v4_to_v6
    if (lpm_insert(&config->mappings.v4_to_v6, 4, (uint8_t*)&ip4, (uint8_t*)&ip4, config->mappings.count - 1)) {
        errno = ENOMEM;
        return -1;
    }

    RTE_LOG(DEBUG, NAT64CP,
            "Added mapping IPv4 -> IPv6: " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT "\n",
            IPv4_BYTES_LE(ip4), IPv6_BYTES(ip6));

    return config->mappings.count - 1;
}

int
nat64_module_config_add_prefix(
    struct module_data *module_data,
    uint8_t prefix[12]
) {
    struct nat64_module_config *config = container_of(
        module_data, struct nat64_module_config, module_data
    );

    // Expand prefix array
    struct nat64_prefix *prefixes = ADDR_OF(&config->prefixes.prefixes);
    if (mem_array_expand_exp(
            &config->module_data.memory_context,
            (void **)&prefixes,
            sizeof(*prefixes),
            &config->prefixes.count
        )) {
        errno = ENOMEM;
        return -1;
    }

    // Add new prefix
    prefixes[config->prefixes.count - 1] = (struct nat64_prefix){
    };
    memcpy(prefixes[config->prefixes.count - 1].prefix, prefix, 12);
    SET_OFFSET_OF(&config->prefixes.prefixes, prefixes);

    LOG_DBG(NAT64CP,
            "Added prefix %02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x\n",
            prefix[0], prefix[1], prefix[2], prefix[3],
            prefix[4], prefix[5], prefix[6], prefix[7],
            prefix[8], prefix[9], prefix[10], prefix[11]);

    return config->prefixes.count - 1;
}
