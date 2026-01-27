#include "balancer.h"
#include "api/agent.h"
#include "graph.h"
#include "handler/info.h"
#include "session.h"
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
#include "handler/vs.h"
#include "state/real.h"
#include "state/session_table.h"
#include "state/state.h"
#include "vs.h"

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

extern void
free_internal_balancer_config(
	struct balancer_config *config, struct memory_context *mctx
);

struct balancer_handle *
balancer_create(
	struct agent *agent, const char *name, struct balancer_config *config
) {
	agent_clean_error(agent);

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	struct memory_context *mctx = &agent->memory_context;

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
		config->state.table_capacity
	);
	if (init_state_result != 0) {
		PUSH_ERROR("failed to initialize balancer state");
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

	return &balancer->handle;

error:
	diag_fill(&agent->diag);

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
		packet_handler_update_reals(handler, count, update),
		"failed to update reals in packet handler"
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_info(
	struct balancer_handle *handle, struct balancer_info *info, uint32_t now
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	packet_handler_balancer_info(handler, info, now);
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_stats(
	struct balancer_handle *handle,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);

	if (ref->device == NULL) {
		NEW_ERROR("device is required");
		goto err;
	}

	if (ref->pipeline == NULL) {
		NEW_ERROR("pipeline is required");
		goto err;
	}

	if (ref->function == NULL) {
		NEW_ERROR("function is required");
		goto err;
	}

	if (ref->chain == NULL) {
		NEW_ERROR("chain is required");
		goto err;
	}

	// Reset diagnostics only after all validation passes
	diag_reset(&balancer->diag);

	// no error
	packet_handler_fill_stats(handler, stats, ref);

	return 0;

err:
	diag_fill(&balancer->diag);
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_sessions(
	struct balancer_handle *handle, struct sessions *sessions, uint32_t now
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	struct named_session_info *sessions_info;
	size_t count =
		packet_handler_sessions_info(handler, &sessions_info, now);
	*sessions = (struct sessions){.sessions_count = count,
				      .sessions = sessions_info};
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_stats_free(struct balancer_stats *stats) {
	if (stats->vs_count > 0) {
		struct named_vs_stats *first_vs = &stats->vs[0];
		struct named_real_stats *reals = first_vs->reals;
		free(reals);
	}
	free(stats->vs);
}

void
balancer_sessions_free(struct sessions *sessions) {
	free(sessions->sessions);
}

void
balancer_info_free(struct balancer_info *info) {
	if (info->vs_count > 0) {
		struct named_vs_info *first_vs = &info->vs[0];
		struct named_real_info *reals = first_vs->reals;
		free(reals);
	}
	free(info->vs);
}

void
balancer_graph_free(struct balancer_graph *graph) {
	for (size_t i = 0; i < graph->vs_count; i++) {
		free(graph->vs[i].reals);
	}
	free(graph->vs);
}

void
balancer_graph(struct balancer_handle *handle, struct balancer_graph *graph) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	struct balancer_state *state = &balancer->state;

	// Allocate VS array
	graph->vs_count = handler->vs_count;
	graph->vs = calloc(graph->vs_count, sizeof(struct graph_vs));
	if (graph->vs == NULL) {
		graph->vs_count = 0;
		return;
	}

	// Iterate through each virtual service
	struct vs *vss = ADDR_OF(&handler->vs);
	for (size_t vs_idx = 0; vs_idx < handler->vs_count; vs_idx++) {
		struct vs *vs = &vss[vs_idx];
		struct graph_vs *graph_vs = &graph->vs[vs_idx];

		// Copy VS identifier
		graph_vs->identifier = vs->identifier;

		// Allocate reals array for this VS
		graph_vs->real_count = vs->reals_count;
		graph_vs->reals =
			calloc(vs->reals_count, sizeof(struct graph_real));
		if (graph_vs->reals == NULL) {
			graph_vs->real_count = 0;
			continue;
		}

		// Iterate through each real in this VS
		const struct real *reals = ADDR_OF(&vs->reals);
		for (size_t real_idx = 0; real_idx < vs->reals_count;
		     real_idx++) {
			const struct real *real = &reals[real_idx];
			struct graph_real *graph_real =
				&graph_vs->reals[real_idx];

			// Copy real identifier (relative to VS)
			graph_real->identifier = real->identifier;

			// Get weight and enabled flag from balancer_state
			struct real_state *real_state =
				balancer_state_get_real_by_idx(
					state, real->registry_idx
				);
			graph_real->weight = real_state->weight;
			graph_real->enabled = real_state->enabled;
		}
	}
}

size_t
balancer_session_table_capacity(struct balancer_handle *handle) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct balancer_state *state = &balancer->state;
	return session_table_capacity(&state->session_table);
}

int
balancer_real_ph_idx(
	struct balancer_handle *handle,
	struct real_identifier *real,
	struct real_ph_index *real_idx
) {
	struct balancer *balancer = balancer_handle_deref(handle);
	struct packet_handler *handler = ADDR_OF(&balancer->handler);
	return packet_handler_real_idx(handler, real, real_idx);
}