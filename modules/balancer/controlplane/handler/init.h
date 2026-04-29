#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"

struct packet_handler;
struct counter_registry;
struct memory_context;
struct packet_handler_config;
struct balancer_state;

struct real;

/**
 * Initialize packet handler counters.
 *
 * @param handler Packet handler instance
 * @param registry Counter registry
 * @param err Error output parameter
 * @return 0 on success, -1 on error
 */
int
init_counters(
	struct packet_handler *handler,
	struct counter_registry *registry,
	yanet_error **err
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
 * @param err Error output parameter
 * @return 0 on success, -1 on error
 */
int
init_decaps(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	yanet_error **err
);

/**
 * Setup reals index mapping.
 *
 * @param handler Packet handler instance
 * @param mctx Memory context
 * @param reals Array of reals
 * @param reals_count Number of reals
 * @param err Error output parameter
 * @return 0 on success, -1 on error
 */
int
setup_reals_index(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct real *reals,
	size_t reals_count,
	yanet_error **err
);

int
init_reals(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	size_t *initial_vs_idx,
	size_t workers,
	yanet_error **err
);