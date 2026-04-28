#include "manager.h"
#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/errors/errors.h"
#include "modules/balancer/controlplane/api/balancer.h"
#include "modules/balancer/controlplane/api/handler.h"
#include "modules/balancer/controlplane/api/real.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Forward declarations for functions in config.c
int
clone_balancer_config_to_relative(
	struct balancer_config *dst,
	struct balancer_config *src,
	struct memory_context *mctx
);
int
clone_balancer_config_from_relative(
	struct balancer_config *dst, struct balancer_config *src
);

struct balancer_agent;

struct balancer_manager {
	struct balancer_handle *balancer;
	struct balancer_manager_config config;
	struct balancer_agent *agent;
};

////////////////////////////////////////////////////////////////////////////////

static struct memory_context *
balancer_manager_memory_context(struct balancer_manager *manager) {
	struct balancer_agent *balancer_agent = ADDR_OF(&manager->agent);
	struct agent *agent = (struct agent *)balancer_agent;
	return &agent->memory_context;
}

static void
setup_session_table_capacity(struct balancer_manager *manager) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	manager->config.balancer.state.table_capacity =
		balancer_session_table_capacity(balancer);
}

////////////////////////////////////////////////////////////////////////////////

static void
clone_manager_config_from_relative(
	struct balancer_manager_config *dst, struct balancer_manager_config *src
) {
	// Clone balancer config
	clone_balancer_config_from_relative(&dst->balancer, &src->balancer);

	// Copy WLC scalar fields
	dst->wlc.power = src->wlc.power;
	dst->wlc.max_real_weight = src->wlc.max_real_weight;
	dst->wlc.vs_count = src->wlc.vs_count;

	// Clone WLC vs array from relative pointers to normal pointers
	if (src->wlc.vs_count > 0) {
		uint32_t *src_vs = ADDR_OF(&src->wlc.vs);
		dst->wlc.vs = calloc(src->wlc.vs_count, sizeof(uint32_t));
		memcpy(dst->wlc.vs, src_vs, sizeof(uint32_t) * src->wlc.vs_count
		);
	} else {
		dst->wlc.vs = NULL;
	}

	// Copy remaining scalar fields
	dst->refresh_period = src->refresh_period;
	dst->max_load_factor = src->max_load_factor;
}

////////////////////////////////////////////////////////////////////////////////

static int
clone_manager_config_to_relative(
	struct balancer_manager_config *dst,
	struct balancer_manager_config *src,
	struct memory_context *mctx,
	yanet_error **err
) {
	// Clone balancer config
	if (clone_balancer_config_to_relative(
		    &dst->balancer, &src->balancer, mctx
	    ) != 0) {
		yanet_error_add(err, "failed to clone balancer config");
		return -1;
	}

	// Copy WLC scalar fields
	dst->wlc.power = src->wlc.power;
	dst->wlc.max_real_weight = src->wlc.max_real_weight;
	dst->wlc.vs_count = src->wlc.vs_count;

	// Clone WLC vs array to relative pointers
	if (src->wlc.vs_count > 0) {
		uint32_t *vs_array = memory_balloc(
			mctx, sizeof(uint32_t) * src->wlc.vs_count
		);
		if (vs_array == NULL) {
			yanet_error_add(err, "failed to allocate wlc vs array");
			return -1;
		}
		memcpy(vs_array,
		       src->wlc.vs,
		       sizeof(uint32_t) * src->wlc.vs_count);
		SET_OFFSET_OF(&dst->wlc.vs, vs_array);
	} else {
		SET_OFFSET_OF(&dst->wlc.vs, NULL);
	}

	// Copy remaining scalar fields
	dst->refresh_period = src->refresh_period;
	dst->max_load_factor = src->max_load_factor;

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

const char *
balancer_manager_name(struct balancer_manager *manager) {
	return balancer_name(ADDR_OF(&manager->balancer));
}

void
balancer_manager_config(
	struct balancer_manager *manager, struct balancer_manager_config *config
) {
	clone_manager_config_from_relative(config, &manager->config);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_manager_update_reals(
	struct balancer_manager *manager,
	size_t count,
	struct real_update *updates,
	yanet_error **err
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	int res = balancer_update_reals(balancer, count, updates, err);
	if (res != 0) {
		return -1;
	}

	struct balancer_config *config = &manager->config.balancer;
	struct packet_handler_config *handler_config = &config->handler;

	for (size_t i = 0; i < count; i++) {
		struct real_update *update = &updates[i];
		if (update->weight != DONT_UPDATE_REAL_WEIGHT) {
			struct real_ph_index index;
			int ec = balancer_real_ph_idx(
				balancer, &update->identifier, &index, err
			);
			if (ec != 0) {
				return -1;
			}

			struct named_vs_config *vs_config =
				ADDR_OF(&handler_config->vs) + index.vs_idx;
			struct named_real_config *real_config =
				ADDR_OF(&vs_config->config.reals) +
				index.real_idx;

			real_config->config.weight = update->weight;
		}
	}

	return 0;
}

int
balancer_manager_update_reals_wlc(
	struct balancer_manager *manager,
	size_t count,
	struct real_update *updates,
	yanet_error **err
) {
	// Validate that WLC updates only change weights, not enable state
	for (size_t i = 0; i < count; i++) {
		struct real_update *update = &updates[i];
		if (update->enabled != DONT_UPDATE_REAL_ENABLED) {
			yanet_error_add(
				err,
				"WLC update at index %lu attempts to change "
				"enable state (not allowed)",
				i
			);
			return -1;
		}
	}

	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	int res = balancer_update_reals(balancer, count, updates, err);
	if (res != 0) {
		return -1;
	}

	// Note: Unlike balancer_manager_update_reals(), this function does NOT
	// update the config weights. The config weight should remain the
	// original static weight. WLC calculations use the config weight as the
	// baseline and adjust the state weight dynamically based on load.

	return 0;
}

int
balancer_manager_update(
	struct balancer_manager *manager,
	struct balancer_manager_config *config,
	struct balancer_update_info *update_info,
	uint32_t now,
	yanet_error **err
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);

	struct balancer_manager_config old_config;
	memcpy(&old_config,
	       &manager->config,
	       sizeof(struct balancer_manager_config));

	// first, try to resize session table
	size_t requested_session_table_capacity =
		config->balancer.state.table_capacity;
	if (requested_session_table_capacity !=
	    manager->config.balancer.state.table_capacity) {
		if (balancer_resize_session_table(
			    balancer, requested_session_table_capacity, now, err
		    ) != 0) {
			yanet_error_add(err, "failed to resize session table");
			goto restore_config_on_error;
		}

		size_t new_session_table_capacity =
			balancer_session_table_capacity(balancer);
		config->balancer.state.table_capacity =
			new_session_table_capacity;
		old_config.balancer.state.table_capacity =
			new_session_table_capacity;
	}

	// clone config
	if (clone_manager_config_to_relative(
		    &manager->config,
		    config,
		    balancer_manager_memory_context(manager),
		    err
	    ) != 0) {
		yanet_error_add(err, "failed to clone config");
		goto restore_config_on_error;
	}

	// update state (resize session table)

	// update packet handler
	if (balancer_update_packet_handler(
		    balancer, &config->balancer.handler, update_info, err
	    ) != 0) {
		yanet_error_add(err, "failed to update packet handler");
		goto restore_config_on_error;
	}

	return 0;

restore_config_on_error:
	memcpy(&manager->config,
	       &old_config,
	       sizeof(struct balancer_manager_config));

	return -1;
}

int
balancer_manager_resize_session_table(
	struct balancer_manager *manager,
	size_t new_size,
	uint32_t now,
	yanet_error **err
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	if (balancer_resize_session_table(balancer, new_size, now, err) != 0) {
		return -1;
	}
	setup_session_table_capacity(manager);
	return 0;
}

int
balancer_manager_info(
	struct balancer_manager *manager,
	struct balancer_info *info,
	uint32_t now
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	if (balancer_info(balancer, info, now) != 0) {
		return -1;
	}
	return 0;
}

void
balancer_manager_sessions(
	struct balancer_manager *manager,
	struct sessions *sessions,
	uint32_t now
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	balancer_sessions(balancer, sessions, now);
}

int
balancer_manager_stats(
	struct balancer_manager *manager,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref,
	yanet_error **err
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	if (balancer_stats(balancer, stats, ref, err) != 0) {
		return -1;
	}
	return 0;
}

void
balancer_manager_graph(
	struct balancer_manager *manager, struct balancer_graph *graph
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	balancer_graph(balancer, graph);
}

////////////////////////////////////////////////////////////////////////////////

extern const char *agent_name;
extern const char *storage_name;

void
balancer_agent_managers(
	struct balancer_agent *agent, struct balancer_managers *managers
) {
	struct balancer_managers *stored_managers =
		agent_storage_read((struct agent *)agent, storage_name);
	assert(stored_managers != NULL);
	managers->count = stored_managers->count;
	managers->managers =
		calloc(managers->count, sizeof(struct balancer_manager *));

	struct balancer_manager **stored_managers_array =
		ADDR_OF(&stored_managers->managers);

	for (size_t i = 0; i < managers->count; ++i) {
		managers->managers[i] = ADDR_OF(stored_managers_array + i);
	}
}

static int
find_manager(struct balancer_agent *balancer_agent, const char *name) {
	struct balancer_managers *stored_managers = agent_storage_read(
		(struct agent *)balancer_agent, storage_name
	);
	assert(stored_managers != NULL);
	struct balancer_manager **managers =
		ADDR_OF(&stored_managers->managers);
	for (size_t i = 0; i < stored_managers->count; ++i) {
		struct balancer_manager *manager = ADDR_OF(managers + i);
		if (strcmp(name, balancer_manager_name(manager)) == 0) {
			return 1;
		}
	}
	return 0;
}

struct balancer_manager *
balancer_agent_new_manager(
	struct balancer_agent *balancer_agent,
	const char *name,
	struct balancer_manager_config *config,
	yanet_error **err
) {
	struct agent *agent = (struct agent *)balancer_agent;

	if (find_manager(balancer_agent, name) != 0) {
		// This is a validation error before creation - cannot store in
		// manager since it doesn't exist yet
		yanet_error_add(err, "manager '%s' already exists", name);
		return NULL;
	}

	struct memory_context *mctx = &agent->memory_context;
	struct balancer_manager *new_manager =
		memory_balloc(mctx, sizeof(struct balancer_manager));
	if (new_manager == NULL) {
		yanet_error_add(err, "failed to allocate manager");
		return NULL;
	}

	memset(new_manager, 0, sizeof(struct balancer_manager));
	SET_OFFSET_OF(&new_manager->agent, balancer_agent);

	if (clone_manager_config_to_relative(
		    &new_manager->config, config, mctx, err
	    ) != 0) {
		yanet_error_add(err, "failed to clone config");
		memory_bfree(
			mctx, new_manager, sizeof(struct balancer_manager)
		);
		return NULL;
	}

	struct balancer_managers *stored_managers = agent_storage_read(
		(struct agent *)balancer_agent, storage_name
	);
	assert(stored_managers != NULL);

	struct balancer_manager **new_managers = memory_balloc(
		mctx,
		sizeof(struct balancer_manager *) * (stored_managers->count + 1)
	);
	if (new_managers == NULL) {
		yanet_error_add(err, "failed to allocate managers array");
		memory_bfree(
			mctx, new_manager, sizeof(struct balancer_manager)
		);
		return NULL;
	}
	for (size_t i = 0; i < stored_managers->count; ++i) {
		EQUATE_OFFSET(
			new_managers + i,
			ADDR_OF(&stored_managers->managers) + i
		);
	}

	struct balancer_handle *handle =
		balancer_create(agent, name, &config->balancer, err);
	if (handle == NULL) {
		memory_bfree(
			mctx,
			new_managers,
			sizeof(struct balancer_manager *) *
				(stored_managers->count + 1)
		);
		memory_bfree(
			mctx, new_manager, sizeof(struct balancer_manager)
		);
		return NULL;
	}

	// Add manager to the list AFTER successful balancer_create
	SET_OFFSET_OF(new_managers + stored_managers->count, new_manager);

	memory_bfree(
		mctx,
		ADDR_OF(&stored_managers->managers),
		sizeof(struct balancer_manager *) * stored_managers->count
	);

	SET_OFFSET_OF(&stored_managers->managers, new_managers);

	++stored_managers->count;

	SET_OFFSET_OF(&new_manager->balancer, handle);

	return new_manager;
}

////////////////////////////////////////////////////////////////////////////////
// Memory Management
////////////////////////////////////////////////////////////////////////////////

void
balancer_manager_info_free(struct balancer_info *info) {
	balancer_info_free(info);
}

void
balancer_manager_sessions_free(struct sessions *sessions) {
	balancer_sessions_free(sessions);
}

void
balancer_manager_stats_free(struct balancer_stats *stats) {
	balancer_stats_free(stats);
}

void
balancer_manager_graph_free(struct balancer_graph *graph) {
	balancer_graph_free(graph);
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_manager_inspect(
	struct balancer_manager *manager, struct balancer_inspect *inspect
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	balancer_inspect(balancer, inspect);
}

void
balancer_manager_inspect_free(struct balancer_inspect *inspect) {
	if (inspect == NULL) {
		return;
	}

	// Free packet handler inspect nested structures
	if (inspect->packet_handler_inspect.vs_ipv4_inspect.vs_inspects !=
	    NULL) {
		free(inspect->packet_handler_inspect.vs_ipv4_inspect.vs_inspects
		);
		inspect->packet_handler_inspect.vs_ipv4_inspect.vs_inspects =
			NULL;
	}

	if (inspect->packet_handler_inspect.vs_ipv6_inspect.vs_inspects !=
	    NULL) {
		free(inspect->packet_handler_inspect.vs_ipv6_inspect.vs_inspects
		);
		inspect->packet_handler_inspect.vs_ipv6_inspect.vs_inspects =
			NULL;
	}
}

void
balancer_manager_active_sessions(
	struct balancer_manager *manager, struct balancer_info *info
) {
	struct balancer_handle *balancer = ADDR_OF(&manager->balancer);
	balancer_active_sessions(balancer, info);
}
