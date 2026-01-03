#include "balancer.h"
#include "state.h"

#include "api/counter.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_address.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/controlplane/diag/diag.h"

#include "handler/handler.h"
#include "state/state.h"
#include "state/vs.h"

#include <assert.h>
#include <stdlib.h>
#include <string.h>

struct balancer_handle {};

struct balancer {
	struct balancer_handle handle;
	struct balancer_state state;
	struct packet_handler *handler;
	struct diag diag;
};

struct balancer *
balancer_handle_deref(struct balancer_handle *handle) {
	return container_of(handle, struct balancer, handle);
}

struct balancer_handle **
balancers(struct agent *agent, size_t *handle_count) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);

	size_t count = 0;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_module_registry *module_registry =
		&cp_config_gen->module_registry;
	size_t registry_size = cp_module_registry_size(module_registry);
	for (size_t i = 0; i < registry_size; ++i) {
		struct cp_module *cp_module =
			cp_module_registry_get(module_registry, i);
		if (cp_module != NULL &&
		    strcmp(cp_module->type, "balancer") == 0) {
			++count;
		}
	}
	*handle_count = count;

	struct balancer_handle **balancers =
		malloc(count * sizeof(struct balancer_handle *));

	size_t idx = 0;
	for (size_t i = 0; i < registry_size; ++i) {
		struct cp_module *cp_module =
			cp_module_registry_get(module_registry, i);
		if (cp_module != NULL &&
		    strcmp(cp_module->type, "balancer") == 0) {
			struct packet_handler *packet_handler = container_of(
				cp_module, struct packet_handler, cp_module
			);
			struct balancer_state *state =
				ADDR_OF(&packet_handler->state);
			struct balancer *balancer =
				container_of(state, struct balancer, state);
			assert(&balancer->state == state);
			assert(balancer->handler == packet_handler);
			balancers[idx++] = &balancer->handle;
		}
	}

	cp_config_unlock(cp_config);

	return balancers;
}

const char *
balancer_take_error_msg(struct balancer_handle *handle) {
	struct balancer *balancer = balancer_handle_deref(handle);
	return diag_take_msg(&balancer->diag);
}

const char *
balancer_name(struct balancer_handle *handle) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	return handler->cp_module.name;
}

int
balancer_resize_session_table(
	struct balancer_handle *handle, size_t new_size, uint32_t now
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	return DIAG_TRY(
		&balancer->diag,
		balancer_state_resize_session_table(
			&balancer->state, new_size, now
		)
	);
}

static bool
balancer_exists(struct cp_config *cp_config, const char *name) {
	bool exists = false;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_module_registry *module_registry =
		&cp_config_gen->module_registry;
	size_t registry_size = cp_module_registry_size(module_registry);
	for (size_t i = 0; i < registry_size; ++i) {
		struct cp_module *cp_module =
			cp_module_registry_get(module_registry, i);
		if (cp_module != NULL &&
		    strcmp(cp_module->type, "balancer") == 0 &&
		    strcmp(cp_module->name, name) == 0) {
			exists = true;
			break;
		}
	}
	return exists;
}

struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct balancer_config *config,
	struct diag *diag
) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	cp_config_lock(cp_config);
	struct memory_context *mctx = &agent->memory_context;
	if (balancer_exists(cp_config, name)) {
		NEW_ERROR("balancer with name '%s' already exists", name);
		goto error;
	}
	struct balancer *balancer =
		memory_balloc(mctx, sizeof(struct balancer));
	if (balancer == NULL) {
		NEW_ERROR("no memory");
		goto error;
	}
	assert((uintptr_t)balancer % alignof(struct balancer) == 0);
	memset(balancer, 0, sizeof(struct balancer));

	int init_state_result = balancer_state_init(
		&balancer->state,
		mctx,
		dp_config->worker_count,
		config->state.table_size
	);
	if (init_state_result != 0) {
		PUSH_ERROR("failed to init balancer state");
		memory_bfree(mctx, balancer, sizeof(struct balancer));
		goto error;
	}

	struct packet_handler *handler = packet_handler_setup(
		agent, name, &config->handler, &balancer->state
	);
	if (handler == NULL) {
		PUSH_ERROR("failed to setup packet handler");
		balancer_state_free(&balancer->state);
		memory_bfree(mctx, balancer, sizeof(struct balancer));
		goto error;
	}

	SET_OFFSET_OF(&balancer->handler, handler);

	cp_config_unlock(cp_config);

	return &balancer->handle;

error:
	diag_fill(diag);

	cp_config_unlock(cp_config);

	return NULL;
}

int
balancer_update_packet_handler(
	struct balancer_handle *handle, struct packet_handler_config *config
) {
	int ret;
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *current_handler = ADDR_OF(&balancer->handler);

	const char *name = current_handler->cp_module.name;

	struct agent *agent = ADDR_OF(&current_handler->cp_module.agent);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);

	struct packet_handler *handler =
		packet_handler_setup(agent, name, config, &balancer->state);
	if (handler == NULL) {
		PUSH_ERROR("failed to setup packet handler");
		diag_fill(&balancer->diag);
		ret = -1;
	} else {
		diag_reset(&balancer->diag);
		SET_OFFSET_OF(&balancer->handler, handler);
		memory_bfree(
			&agent->memory_context,
			current_handler,
			sizeof(struct packet_handler)
		);
		ret = 0;
	}

	cp_config_unlock(cp_config);

	return ret;
}

int
balancer_update_reals(
	struct balancer_handle *handle, size_t count, struct real_update *update
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	return DIAG_TRY(
		&balancer->diag,
		packet_handler_update_reals(handler, count, update)
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_virtual_service_info(
	struct balancer_handle *handle,
	struct named_vs_info *info,
	struct vs_identifier *identifier
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	struct vs_state *vs = balancer_state_find_vs(state, identifier);
	if (vs == NULL) {
		NEW_ERROR("virtual service not found");
		diag_fill(&balancer->diag);
		return -1;
	} else {
		diag_reset(&balancer->diag);
	}
	vs_get_info(vs, info);
	return 0;
}

ssize_t
balancer_virtual_services_info(
	struct balancer_handle *handle, struct named_vs_info **services
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	size_t count = balancer_state_vs_count(state);
	struct named_vs_info *infos =
		malloc(count * sizeof(struct named_vs_info));
	for (size_t i = 0; i < count; ++i) {
		struct vs_state *vs = balancer_state_get_vs_by_idx(state, i);
		vs_get_info(vs, &infos[i]);
	}
	*services = infos;
	return count;
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_real_info(
	struct balancer_handle *handle,
	struct named_real_info *info,
	struct real_identifier *identifier
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	struct real_state *real = balancer_state_find_real(state, identifier);
	if (real == NULL) {
		NEW_ERROR("real not found");
		diag_fill(&balancer->diag);
		return -1;
	} else {
		diag_reset(&balancer->diag);
	}
	real_get_info(real, info);
	return 0;
}

// Returns -1 on error
// and number of real infos on success
ssize_t
balancer_reals_info(
	struct balancer_handle *handle, struct named_real_info **reals
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	size_t count = balancer_state_reals_count(state);
	struct named_real_info *infos =
		malloc(count * sizeof(struct named_real_info));
	for (size_t i = 0; i < count; ++i) {
		struct real_state *real = balancer_state_get_real_by_idx(state, i);
		real_get_info(real, &infos[i]);
	}
	*reals = infos;
	return count;
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_packet_handler_stats(
	struct balancer_handle *handle,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);

	packet_handler_fill_stats(handler, stats, ref);

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_info_free(struct balancer_info *info) {
	free(info->reals);
	free(info->vs);
}

ssize_t
balancer_sessions_info(
	struct balancer_handle *handle,
	struct named_session_info **sessions,
	uint32_t now,
	bool only_count
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	return balancer_state_sessions_info(state, sessions, now, only_count);
}