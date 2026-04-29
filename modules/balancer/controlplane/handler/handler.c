#include "handler.h"
#include "api/balancer.h"
#include "api/session.h"
#include "api/vs.h"
#include "common/memory.h"
#include "common/memory_address.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"

#include "modules/balancer/dataplane/active_sessions.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

#include "api/handler.h"
#include "counters/counters.h"
#include "init.h"
#include "real.h"
#include "rules.h"
#include "services.h"
#include "state/state.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

static int
prepare_vs_configs(
	size_t **initial_vs_idx,
	size_t *ipv4_count,
	size_t *ipv6_count,
	struct packet_handler_config *config,
	yanet_error **err
) {
	*initial_vs_idx = malloc(config->vs_count * sizeof(size_t));
	for (size_t idx = 0; idx < config->vs_count; ++idx) {
		(*initial_vs_idx)[idx] = idx;
	}

	if (validate_and_reorder_vs_configs(
		    *initial_vs_idx,
		    config->vs_count,
		    config->vs,
		    ipv4_count,
		    ipv6_count,
		    err
	    ) != 0) {
		yanet_error_add(
			err, "failed to validate virtual service configs"
		);
		free(*initial_vs_idx);
		return -1;
	}

	return 0;
}

static int
register_and_prepare_all_vs(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct packet_handler_config *config,
	struct vs *virtual_services,
	size_t *initial_vs_idx,
	size_t ipv4_count,
	size_t ipv6_count,
	struct balancer_update_info *update_info,
	int *reuse_ipv4_filter,
	int *reuse_ipv6_filter,
	yanet_error **err
) {
	// Register and prepare IPv4 services
	if (register_and_prepare_vs(
		    handler,
		    prev_handler,
		    IPPROTO_IP,
		    ipv4_count,
		    config->vs,
		    initial_vs_idx,
		    virtual_services,
		    update_info,
		    reuse_ipv4_filter,
		    err
	    ) != 0) {
		yanet_error_add(
			err, "failed to register IPv4 virtual services"
		);
		return -1;
	}

	// Register and prepare IPv6 services
	if (register_and_prepare_vs(
		    handler,
		    prev_handler,
		    IPPROTO_IPV6,
		    ipv6_count,
		    config->vs + ipv4_count,
		    initial_vs_idx + ipv4_count,
		    virtual_services + ipv4_count,
		    update_info,
		    reuse_ipv6_filter,
		    err
	    ) != 0) {
		yanet_error_add(
			err, "failed to register IPv6 virtual services"
		);
		return -1;
	}

	return 0;
}

static int
init_all_packet_handler_vs(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	struct real *reals,
	size_t *initial_vs_idx,
	size_t ipv4_count,
	struct balancer_update_info *update_info,
	yanet_error **err
) {
	size_t reals_counter = 0;

	// Initialize IPv4 packet handler VS
	if (init_packet_handler_vs(
		    handler,
		    IPPROTO_IP,
		    mctx,
		    config->vs,
		    registry,
		    prev_handler,
		    reals,
		    &reals_counter,
		    update_info,
		    initial_vs_idx,
		    err
	    ) != 0) {
		yanet_error_add(
			err, "failed to initialize IPv4 packet handler"
		);
		return -1;
	}

	// Initialize IPv6 packet handler VS
	if (init_packet_handler_vs(
		    handler,
		    IPPROTO_IPV6,
		    mctx,
		    config->vs + ipv4_count,
		    registry,
		    prev_handler,
		    reals,
		    &reals_counter,
		    update_info,
		    initial_vs_idx + ipv4_count,
		    err
	    ) != 0) {
		yanet_error_add(
			err, "failed to initialize IPv6 packet handler"
		);
		return -1;
	}

	return 0;
}

static int
init_all_vs_filters_and_announce(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	size_t *initial_vs_idx,
	size_t ipv4_count,
	int reuse_ipv4_filter,
	int reuse_ipv6_filter,
	yanet_error **err
) {
	// Initialize IPv4 VS filter
	if (init_vs_filter(
		    &handler->vs_ipv4,
		    get_packet_handler_vs(prev_handler, IPPROTO_IP),
		    initial_vs_idx,
		    config->vs,
		    reuse_ipv4_filter,
		    mctx,
		    IPPROTO_IP,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize IPv4 filter");
		return -1;
	}

	// Initialize IPv6 VS filter
	if (init_vs_filter(
		    &handler->vs_ipv6,
		    get_packet_handler_vs(prev_handler, IPPROTO_IPV6),
		    initial_vs_idx + ipv4_count,
		    config->vs + ipv4_count,
		    reuse_ipv6_filter,
		    mctx,
		    IPPROTO_IPV6,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize IPv6 filter");
		return -1;
	}

	// Initialize IPv4 announce
	if (init_announce(
		    &handler->vs_ipv4,
		    mctx,
		    config->vs,
		    IPPROTO_IP,
		    initial_vs_idx,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize IPv4 announce");
		return -1;
	}

	// Initialize IPv6 announce
	if (init_announce(
		    &handler->vs_ipv6,
		    mctx,
		    config->vs + ipv4_count,
		    IPPROTO_IPV6,
		    initial_vs_idx + ipv4_count,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize IPv6 announce");
		return -1;
	}

	return 0;
}

static int
init_vs_and_reals(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	struct packet_handler *prev_handler,
	struct balancer_update_info *update_info,
	size_t workers,
	yanet_error **err
) {

	size_t *initial_vs_idx = NULL;
	size_t ipv4_count = 0;
	size_t ipv6_count = 0;

	// Prepare and validate VS configs
	if (prepare_vs_configs(
		    &initial_vs_idx, &ipv4_count, &ipv6_count, config, err
	    ) != 0) {
		return -1;
	}

	// Collect VS identifiers for registry initialization
	struct vs_identifier *vs_identifiers =
		malloc(sizeof(struct vs_identifier) * config->vs_count);
	if (vs_identifiers == NULL && config->vs_count > 0) {
		yanet_error_add(err, "failed to allocate vs_identifiers");
		goto free_initial_vs_idx_on_error;
	}
	for (size_t i = 0; i < config->vs_count; ++i) {
		vs_identifiers[i] = config->vs[i].identifier;
	}

	// Initialize VS registry
	if (vs_registry_init(
		    &handler->vs_registry,
		    mctx,
		    vs_identifiers,
		    config->vs_count,
		    prev_handler ? &prev_handler->vs_registry : NULL,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize VS registry");
		free(vs_identifiers);
		goto free_initial_vs_idx_on_error;
	}
	free(vs_identifiers);

	// Initialize reals
	if (init_reals(
		    handler,
		    prev_handler,
		    mctx,
		    config,
		    registry,
		    initial_vs_idx,
		    workers,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to initialize reals");
		goto free_vs_registry_on_error;
	}

	struct real *reals = ADDR_OF(&handler->reals);

	// Allocate virtual services array
	handler->vs_count = config->vs_count;
	struct vs *virtual_services =
		memory_balloc(mctx, sizeof(struct vs) * config->vs_count);
	if (virtual_services == NULL && config->vs_count > 0) {
		yanet_error_add(err, "failed to allocate virtual services");
		goto free_vs_registry_on_error;
	}
	SET_OFFSET_OF(&handler->vs, virtual_services);

	// Register and prepare all VS (both IPv4 and IPv6)
	int reuse_ipv4_filter = 0;
	int reuse_ipv6_filter = 0;
	if (register_and_prepare_all_vs(
		    handler,
		    prev_handler,
		    config,
		    virtual_services,
		    initial_vs_idx,
		    ipv4_count,
		    ipv6_count,
		    update_info,
		    &reuse_ipv4_filter,
		    &reuse_ipv6_filter,
		    err
	    ) != 0) {
		goto free_virtual_services_on_error;
	}

	// Initialize all packet handler VS
	if (init_all_packet_handler_vs(
		    handler,
		    prev_handler,
		    mctx,
		    config,
		    registry,
		    reals,
		    initial_vs_idx,
		    ipv4_count,
		    update_info,
		    err
	    ) != 0) {
		goto free_virtual_services_on_error;
	}

	// Initialize all VS filters and announce
	if (init_all_vs_filters_and_announce(
		    handler,
		    prev_handler,
		    mctx,
		    config,
		    initial_vs_idx,
		    ipv4_count,
		    reuse_ipv4_filter,
		    reuse_ipv6_filter,
		    err
	    ) != 0) {
		goto free_virtual_services_on_error;
	}

	// Setup VS index mapping
	if (setup_vs_index(
		    handler, virtual_services, initial_vs_idx, mctx, err
	    ) != 0) {
		yanet_error_add(err, "failed to setup vs index");
		goto free_virtual_services_on_error;
	}

	free(initial_vs_idx);
	return 0;

free_virtual_services_on_error:
	memory_bfree(
		mctx, virtual_services, sizeof(struct vs) * config->vs_count
	);

free_vs_registry_on_error:
	vs_registry_free(&handler->vs_registry);

free_initial_vs_idx_on_error:
	free(initial_vs_idx);
	return -1;
}

#define MAX_TIMEOUT ACTIVE_SESSIONS_TRACKER_MAX_TIMEOUT

static bool
validate_sessions_timeouts(struct sessions_timeouts *timeouts) {
	return (timeouts->tcp <= MAX_TIMEOUT && timeouts->udp <= MAX_TIMEOUT &&
		timeouts->def <= MAX_TIMEOUT &&
		timeouts->tcp_fin <= MAX_TIMEOUT &&
		timeouts->tcp_syn <= MAX_TIMEOUT &&
		timeouts->tcp_syn_ack <= MAX_TIMEOUT);
}

struct packet_handler *
packet_handler_setup(
	struct agent *agent,
	const char *name,
	struct packet_handler_config *config,
	struct balancer_state *state,
	struct packet_handler *prev_handler,
	struct balancer_update_info *update_info,
	yanet_error **err
) {

	if (!validate_sessions_timeouts(&config->sessions_timeouts)) {
		yanet_error_add(
			err,
			"sessions timeouts are too large (max is %d)",
			MAX_TIMEOUT
		);
		return NULL;
	}

	if (update_info != NULL && config->vs_count > 0) {
		update_info->vs_acl_reused =
			calloc(config->vs_count, sizeof(struct vs_identifier));
	}

	struct memory_context *mctx = &agent->memory_context;
	struct packet_handler *handler =
		memory_balloc(mctx, sizeof(struct packet_handler));
	if (handler == NULL) {
		yanet_error_add(err, "failed to allocate packet handler");
		return NULL;
	}
	memset(handler, 0, sizeof(struct packet_handler));
	SET_OFFSET_OF(&handler->state, state);

	memcpy(&handler->sessions_timeouts,
	       &config->sessions_timeouts,
	       sizeof(struct sessions_timeouts));

	if (cp_module_init(&handler->cp_module, agent, "balancer", name, err) !=
	    0) {
		yanet_error_add(
			err, "failed to initialize controlplane module"
		);
		goto free_handler;
	}

	struct counter_registry *counter_registry =
		&handler->cp_module.counter_registry;

	if (init_counters(handler, counter_registry, err) != 0) {
		goto free_handler;
	}

	if (init_sources(handler, mctx, config) != 0) {
		yanet_error_add(err, "failed to setup source addresses");
		goto free_handler;
	}

	if (init_decaps(handler, mctx, config, err) != 0) {
		goto free_handler;
	}

	size_t workers = ADDR_OF(&agent->dp_config)->worker_count;
	if (init_vs_and_reals(
		    handler,
		    mctx,
		    config,
		    counter_registry,
		    prev_handler,
		    update_info,
		    workers,
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to setup vs and reals");
		goto free_decap;
	}

	struct cp_module *cp_module = &handler->cp_module;
	if (agent_update_modules(agent, 1, &cp_module, err) != 0) {
		goto free_vs;
	}

	return handler;

free_vs:
	memory_bfree(
		mctx,
		ADDR_OF(&handler->vs),
		sizeof(struct vs) * handler->vs_count
	);
	map_free(&handler->vs_index);

free_decap:
	lpm_free(&handler->decap_ipv4);
	lpm_free(&handler->decap_ipv6);

free_handler:
	memory_bfree(mctx, handler, sizeof(struct packet_handler));

	return NULL;
}

int
packet_handler_real_idx(
	struct packet_handler *handler,
	struct real_identifier *real,
	struct real_ph_index *real_ph_index,
	yanet_error **err
) {
	// Look up the real's stable index in the registry
	ssize_t stable_idx;
	if ((stable_idx = reals_registry_lookup(&handler->reals_registry, real)
	    ) == -1) {
		yanet_error_add(err, "real not found in registry");
		return -1;
	}

	// Look up the config index from the stable index
	size_t config_idx;
	if (map_find(&handler->reals_index, stable_idx, &config_idx) != 0) {
		yanet_error_add(err, "real not found");
		return -1;
	}

	// Get the real and find its VS
	struct real *reals = ADDR_OF(&handler->reals);
	struct real *r = &reals[config_idx];

	// Look up VS stable index
	ssize_t vs_stable_idx;
	if ((vs_stable_idx = vs_registry_lookup(
		     &handler->vs_registry, &r->identifier.vs_identifier
	     )) == -1) {
		yanet_error_add(err, "vs not found in registry");
		return -1;
	}

	// Look up VS config index
	size_t vs_config_idx;
	if (map_find(&handler->vs_index, vs_stable_idx, &vs_config_idx) != 0) {
		yanet_error_add(err, "virtual service not found");
		return -1;
	}

	real_ph_index->vs_idx = vs_config_idx;

	struct vs *vss = ADDR_OF(&handler->vs);
	struct vs *vs = &vss[vs_config_idx];

	real_ph_index->real_idx = config_idx - vs->first_real_idx;

	return 0;
}

void
packet_handler_free(struct packet_handler *handler) {
	if (handler == NULL) {
		return;
	}

	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;

	// Free VS filters (if not reused)
	free_filter_ipv4(&handler->vs_ipv4, mctx);
	free_filter_ipv6(&handler->vs_ipv6, mctx);

	// Free announce LPMs
	lpm_free(&handler->vs_ipv4.announce);
	lpm_free(&handler->vs_ipv6.announce);

	// Free VS index maps
	map_free(&handler->vs_ipv4.index);
	map_free(&handler->vs_ipv6.index);

	// Free each VS's resources
	struct vs *vss = ADDR_OF(&handler->vs);
	for (size_t i = 0; i < handler->vs_count; ++i) {
		vs_free(&vss[i], mctx);
	}

	// Free VS array
	memory_bfree(mctx, vss, sizeof(struct vs) * handler->vs_count);

	// Free VS index map
	map_free(&handler->vs_index);

	// Free VS registry
	vs_registry_free(&handler->vs_registry);

	// Free reals array
	size_t workers = ADDR_OF(&handler->state)->workers;
	struct real *reals = ADDR_OF(&handler->reals);
	for (size_t i = 0; i < handler->reals_count; ++i) {
		real_free(&reals[i], workers, mctx);
	}
	memory_bfree(mctx, reals, sizeof(struct real) * handler->reals_count);

	// Free reals index map
	map_free(&handler->reals_index);

	// Free reals registry
	reals_registry_free(&handler->reals_registry);

	// Free decap LPMs
	lpm_free(&handler->decap_ipv4);
	lpm_free(&handler->decap_ipv6);

	// Free the handler itself
	memory_bfree(mctx, handler, sizeof(struct packet_handler));
}
