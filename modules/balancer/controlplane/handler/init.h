#pragma once

#include <stddef.h>

struct packet_handler;
struct counter_registry;
struct memory_context;
struct packet_handler_config;
struct balancer_state;

/**
 * Initialize packet handler counters.
 *
 * @param handler Packet handler instance
 * @param registry Counter registry
 * @return 0 on success, -1 on error
 */
int
init_counters(
	struct packet_handler *handler, struct counter_registry *registry
);

/**
 * Initialize source addresses for the packet handler.
 *
 * @param handler Packet handler instance
 * @param mctx Memory context
 * @param config Packet handler configuration
 * @return 0 on success, -1 on error
 */
int
init_sources(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
);

/**
 * Initialize decap addresses for the packet handler.
 *
 * @param handler Packet handler instance
 * @param mctx Memory context
 * @param config Packet handler configuration
 * @return 0 on success, -1 on error
 */
int
init_decaps(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
);

/**
 * Setup reals index mapping.
 *
 * @param handler Packet handler instance
 * @param mctx Memory context
 * @return 0 on success, -1 on error
 */
int
setup_reals_index(struct packet_handler *handler, struct memory_context *mctx);

/**
 * Initialize reals for the packet handler.
 *
 * @param handler Packet handler instance
 * @param state Balancer state
 * @param mctx Memory context
 * @param config Packet handler configuration
 * @param registry Counter registry
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @return 0 on success, -1 on error
 */
int
init_reals(
	struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	size_t *initial_vs_idx
);