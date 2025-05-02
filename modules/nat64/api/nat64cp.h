#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct agent;
struct module_data;
struct memory_context;

/**
 * @brief Initializes NAT64 module configuration
 *
 * Creates and initializes a new NAT64 module configuration structure.
 * Allocates necessary memory and initializes all required data structures.
 *
 * @param agent Pointer to the agent structure
 * @param name Name of the module instance
 * @return Pointer to module_data on success, NULL on failure with errno set:
 *         - ENXIO: Module not found in configuration
 *         - ENOMEM: Memory allocation failed
 */
struct module_data *
nat64_module_config_init(struct agent *agent, const char *name);

struct module_data *
nat64_module_config_init_config(
	struct memory_context *rmemory_context, const char *name, uint64_t index
);

/**
 * @brief Frees NAT64 module configuration resources
 *
 * Releases all resources allocated for the NAT64 module configuration,
 * including LPM structures, mapping arrays, and prefix arrays.
 *
 * @param module_data Pointer to the module data structure
 */
void
nat64_module_config_free(struct module_data *module_data);

/**
 * @brief Adds an IPv4-IPv6 address mapping
 *
 * Creates a new mapping between IPv4 and IPv6 addresses and stores it
 * in both the mapping array and LPM structures.
 *
 * @param module_data Pointer to the module data structure
 * @param ip4 IPv4 address in network byte order
 * @param ip6 IPv6 address (16 bytes)
 * @param prefix_num Index of the prefix to use
 * @return Index of the new mapping on success, -1 on failure with errno set:
 *         - ENOMEM: Memory allocation failed
 *         - EINVAL: Invalid prefix index
 */
int
nat64_module_config_add_mapping(
	struct module_data *module_data,
	uint32_t ip4,
	uint8_t ip6[16],
	size_t prefix_num
);

/**
 * @brief Adds a NAT64 prefix
 *
 * Adds a new IPv6 prefix to be used for NAT64 translation.
 * The prefix is stored in the prefix array and can be referenced
 * by mappings using its index.
 *
 * @param module_data Pointer to the module data structure
 * @param prefix IPv6 prefix (12 bytes)
 * @return Index of the new prefix on success, -1 on failure with errno set:
 *         - ENOMEM: Memory allocation failed
 */
int
nat64_module_config_add_prefix(
	struct module_data *module_data, uint8_t prefix[12]
);

/**
 * @brief Sets drop_unknown_prefix and drop_unknown_mapping flags
 *
 * Configures whether packets with unknown prefixes or mappings should be
 * dropped.
 *
 * @param module_data Pointer to the module data structure
 * @param drop_unknown_prefix Whether to drop packets with unknown prefix
 * @param drop_unknown_mapping Whether to drop packets with unknown mapping
 * @return 0 on success, -1 on failure with errno set:
 *         - EINVAL: Invalid module data pointer
 */
int
nat64_module_config_set_drop_unknown(
	struct module_data *module_data,
	bool drop_unknown_prefix,
	bool drop_unknown_mapping
);
