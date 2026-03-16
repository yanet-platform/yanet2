#pragma once

#include <stddef.h>

struct packet_handler;
struct packet_handler_vs;
struct named_vs_config;
struct balancer_state;
struct counter_registry;
struct memory_context;
struct balancer_update_info;
struct vs;
struct real;

/**
 * Find VS in packet handler VS structure.
 *
 * @param packet_handler_vs Packet handler VS structure
 * @param vs VS to find
 * @return Pointer to VS if found, NULL otherwise
 */
struct vs *
find_vs_in_packet_handler_vs(
	struct packet_handler_vs *packet_handler_vs, struct vs *vs
);

/**
 * Get packet handler VS for a specific protocol.
 *
 * @param handler Packet handler instance
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @return Pointer to packet handler VS structure
 */
struct packet_handler_vs *
get_packet_handler_vs(struct packet_handler *handler, int proto);

/**
 * Check if VS filter can be reused from previous configuration.
 *
 * @param current_vs_count Current VS count
 * @param prev_vs_count Previous VS count
 * @param match_count Number of matching VS
 * @return 1 if filter can be reused, 0 otherwise
 */
int
can_reuse_filter(int current_vs_count, int prev_vs_count, int match_count);

/**
 * Validate and reorder VS configurations.
 * Moves IPv4 services first, then IPv6 services.
 *
 * @param initial_vs_idx Array of initial VS indices (will be reordered)
 * @param count Number of VS configurations
 * @param configs Array of VS configurations (will be reordered)
 * @param ipv4_count Output: number of IPv4 services
 * @param ipv6_count Output: number of IPv6 services
 * @return 0 on success, -1 on error
 */
int
validate_and_reorder_vs_configs(
	size_t *initial_vs_idx,
	size_t count,
	struct named_vs_config *configs,
	size_t *ipv4_count,
	size_t *ipv6_count
);

/**
 * Register virtual services in handler registry.
 *
 * @param handler Packet handler instance
 * @param vs_count Number of virtual services
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @param configs Array of VS configurations
 * @param prev_handler Previous packet handler (may be NULL)
 * @param match Output: number of matching VS with previous config
 * @return 0 on success, -1 on error
 */
int
register_virtual_services(
	struct packet_handler *handler,
	size_t vs_count,
	const size_t *initial_vs_idx,
	struct named_vs_config *configs,
	struct packet_handler *prev_handler,
	size_t *match
);

/**
 * Register and prepare virtual services for a specific protocol.
 *
 * @param handler Packet handler instance
 * @param prev_handler Previous packet handler (may be NULL)
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @param vs_count Number of virtual services
 * @param vs_configs Array of VS configurations
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @param virtual_services Array of VS structures to initialize
 * @param update_info Update information structure (may be NULL)
 * @param reuse_filter Output: 1 if filter can be reused, 0 otherwise
 * @return 0 on success, -1 on error
 */
int
register_and_prepare_vs(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	int proto,
	size_t vs_count,
	struct named_vs_config *vs_configs,
	size_t *initial_vs_idx,
	struct vs *virtual_services,
	struct balancer_update_info *update_info,
	int *reuse_filter
);

/**
 * Initialize packet handler VS for a specific protocol.
 *
 * @param handler Packet handler instance
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @param mctx Memory context
 * @param vs_configs Array of VS configurations
 * @param registry Counter registry
 * @param prev_handler Previous packet handler (may be NULL)
 * @param reals Array of reals
 * @param reals_counter Counter for reals (will be incremented)
 * @param update_info Update information structure (may be NULL)
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @return 0 on success, -1 on error
 */
int
init_packet_handler_vs(
	struct packet_handler *handler,
	int proto,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	struct counter_registry *registry,
	struct packet_handler *prev_handler,
	struct real *reals,
	size_t *reals_counter,
	struct balancer_update_info *update_info,
	size_t *initial_vs_idx
);

/**
 * Initialize VS filter for a packet handler VS.
 *
 * @param packet_handler_vs Packet handler VS structure
 * @param prev_packet_handler_vs Previous packet handler VS (may be NULL)
 * @param vs_configs Array of VS configurations
 * @param reuse_filter 1 if filter should be reused, 0 otherwise
 * @param mctx Memory context
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @return 0 on success, -1 on error
 */
int
init_vs_filter(
	struct packet_handler_vs *packet_handler_vs,
	struct packet_handler_vs *prev_packet_handler_vs,
	struct named_vs_config *vs_configs,
	int reuse_filter,
	struct memory_context *mctx,
	size_t *initial_vs_idx,
	int proto
);

/**
 * Initialize announce LPM for a packet handler VS.
 *
 * @param handler Packet handler VS structure
 * @param mctx Memory context
 * @param vs_configs Array of VS configurations
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @return 0 on success, -1 on error
 */
int
init_announce(
	struct packet_handler_vs *handler,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	int proto
);

/**
 * Setup VS index mapping.
 *
 * @param handler Packet handler instance
 * @param virtual_services Array of VS structures
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @param mctx Memory context
 * @return 0 on success, -1 on error
 */
int
setup_vs_index(
	struct packet_handler *handler,
	struct vs *virtual_services,
	size_t *initial_vs_idx,
	struct memory_context *mctx
);