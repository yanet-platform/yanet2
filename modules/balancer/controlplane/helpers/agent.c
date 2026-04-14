#include <stdalign.h>

#include "agent.h"

#include "api/agent.h"

#include "common/memory_address.h"
#include "common/ttlmap/ttlmap.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"

#include "modules/balancer/dataplane/dataplane.h"
#include "modules/balancer/dataplane/types/session.h"

static const char *storage_name = "balancer_storage";

struct balancer_storage {
	size_t count;
	struct balancer_packet_handler **handlers;
};

static struct balancer_storage *
get_storage(struct agent *agent) {
	return agent_storage_read(agent, storage_name);
}

static int
ensure_storage(struct agent *agent) {
	if (get_storage(agent) != NULL) {
		return 0;
	}

	struct balancer_storage empty = {0};
	return agent_storage_put(agent, storage_name, &empty, sizeof(empty));
}

int
balancer_agent_install(
	struct agent *agent, struct balancer_packet_handler *handler
) {
	struct cp_module *module = &handler->cp_module;
	return agent_update_modules(agent, 1, &module);
}

int
balancer_agent_register(
	struct agent *agent, struct balancer_packet_handler *handler
) {
	if (ensure_storage(agent) != 0) {
		return -1;
	}

	/* Try to find free slot. */
	struct balancer_storage *s = get_storage(agent);
	struct balancer_packet_handler **handlers = ADDR_OF(&s->handlers);
	for (size_t i = 0; i < s->count; ++i) {
		if (handlers[i] == NULL) {
			SET_OFFSET_OF(handlers + i, handler);
			return 0;
		}
	}

	/* No free slot found, allocate new one. */
	struct memory_context *mctx = &agent->memory_context;
	size_t new_count = s->count + 1;
	struct balancer_packet_handler **new_arr = memory_balloc(
		mctx, sizeof(struct balancer_packet_handler *) * new_count
	);
	if (new_arr == NULL) {
		return -1;
	}

	/* Copy existing entries. */
	if (s->count > 0) {
		struct balancer_packet_handler **old_arr =
			ADDR_OF(&s->handlers);
		for (size_t i = 0; i < s->count; ++i) {
			EQUATE_OFFSET(new_arr + i, old_arr + i);
		}

		memory_bfree(
			mctx,
			old_arr,
			sizeof(struct balancer_packet_handler *) * s->count
		);
	}

	/* Append new handler. */
	SET_OFFSET_OF(new_arr + s->count, handler);
	SET_OFFSET_OF(&s->handlers, new_arr);
	s->count = new_count;

	return 0;
}

struct balancer_packet_handler **
balancer_agent_list(struct agent *agent, size_t *count) {
	struct balancer_storage *s = get_storage(agent);
	if (s == NULL) {
		return NULL;
	}

	*count = s->count;

	return ADDR_OF(&s->handlers);
}

void
balancer_agent_forget(
	struct agent *agent, struct balancer_packet_handler *handler
) {
	struct balancer_storage *s = get_storage(agent);
	if (s == NULL) {
		return;
	}

	struct balancer_packet_handler **handlers = ADDR_OF(&s->handlers);

	for (size_t i = 0; i < s->count; ++i) {
		if (ADDR_OF(handlers + i) == handler) {
			handlers[i] = NULL;
			break;
		}
	}
}

struct balancer_session_table *
balancer_agent_create_st(struct agent *agent, size_t capacity) {
	struct memory_context *mctx = &agent->memory_context;

	struct balancer_session_table *st =
		memory_balloc(mctx, sizeof(struct balancer_session_table));
	if (st == NULL) {
		return NULL;
	}
	memset(st, 0, sizeof(*st));

	memory_context_init_from(&st->mctx, mctx, "session_table");

	int res = TTLMAP_INIT(
		&st->maps[0],
		&st->mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		capacity
	);
	if (res != 0) {
		memory_bfree(mctx, st, sizeof(*st));
		return NULL;
	}

	ttlmap_init_empty(&st->maps[1]);
	rcu_init(&st->rcu);
	st->current_gen = 0;
	st->workers = ADDR_OF(&agent->dp_config)->worker_count;

	return st;
}

void
balancer_agent_destroy_st(
	struct agent *agent, struct balancer_session_table *st
) {
	TTLMAP_FREE(&st->maps[0]);
	TTLMAP_FREE(&st->maps[1]);
	struct memory_context *mctx = &agent->memory_context;
	memory_bfree(mctx, st, sizeof(*st));
}
