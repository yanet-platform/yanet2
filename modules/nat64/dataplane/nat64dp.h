#pragma once

#include "config.h"
#include "dataplane/module/module.h"

/**
 * @brief NAT64 module structure
 *
 * This structure represents a NAT64 module instance that implements stateless
 * NAT64 translation according to RFC7915. It handles translation between IPv6
 * and IPv4 networks including address mapping, protocol-specific handling,
 * and fragmentation processing.
 *
 * The module inherits from the base module structure and maintains its own
 * configuration for address mappings, prefixes and MTU settings.
 *
 * @see nat64_module_config
 * @see module
 *
 * @note This implementation focuses on stateless translation. For stateful
 *       NAT64 functionality, refer to RFC6146.
 */
struct nat64_module {
	struct module
		module; /**< Base module structure for common functionality */
	struct nat64_module_config
		*config; /**< NAT64-specific configuration including mappings
			    and prefixes */
};

/**
 * @brief Creates and initializes a new NAT64 module instance
 *
 * This function creates a new NAT64 module instance and initializes it with
 * default settings. The module implements stateless NAT64 translation as
 * specified in RFC7915.
 *
 * The initialization process includes:
 * - Allocating memory for the module structure
 * - Setting up the base module interface
 * - Initializing internal state
 * - Registering packet handlers
 *
 * @return Pointer to the newly created NAT64 module instance on success,
 *         NULL on failure with errno set to indicate the error:
 *         - ENOMEM: Memory allocation failed
 *         - EINVAL: Module initialization failed
 *
 * @note The returned pointer must be cast to struct nat64_module* to access
 *       NAT64-specific fields.
 *
 * @see nat64_module
 * @see nat64_module_config
 *
 * @example
 * ```c
 * // Create new NAT64 module
 * struct module *mod = new_module_nat64();
 * if (!mod) {
 *     fprintf(stderr, "Failed to create NAT64 module: %s\n", strerror(errno));
 *     return -1;
 * }
 *
 * // Cast to nat64_module to access specific fields
 * struct nat64_module *nat64 = (struct nat64_module *)mod;
 * ```
 */
struct module *
new_module_nat64(void);
