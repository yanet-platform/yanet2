#include "l3b_virtual_service_object.h"

#include <errno.h>
#include <stdlib.h>
#include <string.h>

#include <lib/filter/compiler.h>

#include "l3b_session_table_object.h"

#include "common/container_of.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/object/object.h"

// Compiler counterpart of the dataplane query signature (see the l3b module
// process header). Source filter: source network + destination (service)
// port.
FILTER_COMPILER_DECLARE(L3B_SOURCE_FILTER_IP4_TAG, net4_src, port_dst);
FILTER_COMPILER_DECLARE(L3B_SOURCE_FILTER_IP6_TAG, net6_src, port_dst);

// Translate the source filter rules into classifier filter_rule descriptors.
static void
make_source_filter_rules(
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t source_filter_rule_count,
	struct filter_rule *filter_rules
) {
	for (uint32_t idx = 0; idx < source_filter_rule_count; ++idx) {
		const struct l3b_source_filter_rule *rule =
			&source_filter_rules[idx];
		struct filter_rule *filter_rule = &filter_rules[idx];

		filter_rule->net6.src_count = rule->net6s.count;
		filter_rule->net6.srcs = rule->net6s.items;
		filter_rule->net4.src_count = rule->net4s.count;
		filter_rule->net4.srcs = rule->net4s.items;
		filter_rule->transport.dst_count = rule->port_ranges.count;
		filter_rule->transport.dsts = rule->port_ranges.items;
		filter_rule->action = 0;
	}
}

// Build a heap array of pointers to filter_rules for filter_init. Returns NULL
// when count is zero (filter_init accepts a NULL rule list).
static const struct filter_rule **
make_filter_rule_ptrs(
	const struct filter_rule *filter_rules, uint32_t rule_count
) {
	if (rule_count == 0) {
		return NULL;
	}

	const struct filter_rule **filter_rule_ptrs =
		calloc(rule_count, sizeof(struct filter_rule *));
	if (filter_rule_ptrs == NULL) {
		return NULL;
	}

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		filter_rule_ptrs[idx] = &filter_rules[idx];
	}
	return filter_rule_ptrs;
}

static int
build_source_filters(
	struct virtual_service *virtual_service,
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t source_filter_rule_count,
	struct memory_context *memory_context,
	yanet_error **err
) {
	struct filter_rule *filter_rules =
		calloc(source_filter_rule_count, sizeof(struct filter_rule));
	if (filter_rules == NULL && source_filter_rule_count > 0) {
		yanet_error_add(err, "failed to allocate source filter rules");
		return -1;
	}

	make_source_filter_rules(
		source_filter_rules, source_filter_rule_count, filter_rules
	);

	const struct filter_rule **filter_rule_ptrs =
		make_filter_rule_ptrs(filter_rules, source_filter_rule_count);

	int rc = -1;
	if (filter_rule_ptrs == NULL && source_filter_rule_count > 0) {
		yanet_error_add(
			err, "failed to allocate source filter rule ptrs"
		);
		goto out;
	}

	if (filter_init(
		    &virtual_service->filter_ip4,
		    L3B_SOURCE_FILTER_IP4_TAG,
		    filter_rule_ptrs,
		    source_filter_rule_count,
		    memory_context,
		    "source_filter_ip4",
		    err
	    )) {
		yanet_error_add(err, "failed to init source filter_ip4");
		goto out;
	}

	if (filter_init(
		    &virtual_service->filter_ip6,
		    L3B_SOURCE_FILTER_IP6_TAG,
		    filter_rule_ptrs,
		    source_filter_rule_count,
		    memory_context,
		    "source_filter_ip6",
		    err
	    )) {
		yanet_error_add(err, "failed to init source filter_ip6");
		filter_free(
			&virtual_service->filter_ip4, L3B_SOURCE_FILTER_IP4_TAG
		);
		goto out;
	}

	rc = 0;

out:
	free(filter_rule_ptrs);
	free(filter_rules);
	return rc;
}

struct cp_object *
l3b_virtual_service_create(
	const struct l3b_virtual_service_create_config *config,
	struct cp_object **session_table,
	yanet_error **err
) {
	struct agent *agent = config->agent;
	const struct l3b_virtual_service *virtual_service =
		config->virtual_service;
	struct l3b_virtual_service_object *object =
		(struct l3b_virtual_service_object *)memory_balloc(
			&agent->memory_context,
			sizeof(struct l3b_virtual_service_object)
		);
	if (object == NULL) {
		yanet_error_add(
			err, "failed to allocate virtual service object"
		);
		return NULL;
	}

	if (cp_object_init(
		    &object->cp_object,
		    agent,
		    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		    config->name,
		    err
	    )) {
		yanet_error_add(err, "failed to init virtual service object");
		memory_bfree(
			&agent->memory_context,
			object,
			sizeof(struct l3b_virtual_service_object)
		);
		return NULL;
	}

	// Every allocation below is attributed to the object's own memory
	// context so the accounting travels with the object.
	struct memory_context *memory_context =
		&object->cp_object.memory_context;
	struct virtual_service *vs = &object->virtual_service;

	vs->scheduler_hash_mask = virtual_service->hash_mask;
	vs->scheduler_index_mask = virtual_service->index_mask;
	vs->real_server_count = virtual_service->real_server_count;

	// Backends.
	if (virtual_service->real_server_count > 0) {
		struct real_server *real_servers =
			(struct real_server *)memory_balloc(
				memory_context,
				sizeof(struct real_server) *
					virtual_service->real_server_count
			);
		if (real_servers == NULL) {
			yanet_error_add(err, "failed to allocate real servers");
			goto error_vs;
		}

		for (uint32_t idx = 0; idx < virtual_service->real_server_count;
		     ++idx) {
			real_servers[idx].type =
				virtual_service->real_servers[idx].type;
			real_servers[idx].destination_addr =
				virtual_service->real_servers[idx]
					.destination_addr;
			real_servers[idx].source_net =
				virtual_service->real_servers[idx].source_net;
			real_servers[idx].state = real_state_enabled;
		}
		SET_OFFSET_OF(&vs->real_servers, real_servers);
	}

	// Real server ring: allocate capacity slots, count starts empty and is
	// populated later via l3b_virtual_service_update_ring.
	uint32_t ring_capacity = virtual_service->ring_capacity;
	if (ring_capacity > 0) {
		uint32_t *server_indexes = (uint32_t *)memory_balloc(
			memory_context, sizeof(uint32_t) * ring_capacity
		);
		if (server_indexes == NULL) {
			yanet_error_add(
				err, "failed to allocate real server ring"
			);
			goto error_real_servers;
		}
		SET_OFFSET_OF(&vs->real_ring.server_indexes, server_indexes);
	}
	vs->real_ring.capacity = ring_capacity;
	vs->real_ring.count = 0;
	vs->real_ring.sequence = 0;

	// Per-service source filters.
	if (build_source_filters(
		    vs,
		    virtual_service->source_filter_rules,
		    virtual_service->source_filter_rule_count,
		    memory_context,
		    err
	    )) {
		goto error_ring;
	}

	// Session table: adopted from the service being replaced so every
	// pinned flow keeps its real server across the update, or created
	// fresh under the service's name and own object type. Either way the
	// dataplane reaches it through the embedded relative pointer.
	struct cp_object *table = config->adopt_session_table;
	if (table == NULL) {
		table = l3b_session_table_object_create(
			agent,
			config->name,
			config->worker_count,
			virtual_service->session_index_size,
			0,
			err
		);
		if (table == NULL) {
			yanet_error_add(
				err, "failed to create session table object"
			);
			goto error_filters;
		}
	}
	SET_OFFSET_OF(
		&vs->session_table,
		container_of(table, struct l3b_session_table_object, cp_object)
	);

	if (session_table != NULL) {
		*session_table = table;
	}

	return &object->cp_object;

error_filters:
	filter_free(&vs->filter_ip4, L3B_SOURCE_FILTER_IP4_TAG);
	filter_free(&vs->filter_ip6, L3B_SOURCE_FILTER_IP6_TAG);

error_ring:
	if (vs->real_ring.capacity > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_ring.server_indexes),
			sizeof(uint32_t) * vs->real_ring.capacity
		);
	}

error_real_servers:
	if (vs->real_server_count > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_servers),
			sizeof(struct real_server) * vs->real_server_count
		);
	}

error_vs:
	cp_object_fini(&object->cp_object);
	memory_bfree(
		&agent->memory_context,
		object,
		sizeof(struct l3b_virtual_service_object)
	);
	return NULL;
}

static void
l3b_virtual_service_object_destroy(struct cp_object *cp_object) {
	struct l3b_virtual_service_object *object = container_of(
		cp_object, struct l3b_virtual_service_object, cp_object
	);
	struct virtual_service *vs = &object->virtual_service;
	struct memory_context *memory_context = &cp_object->memory_context;

	filter_free(&vs->filter_ip4, L3B_SOURCE_FILTER_IP4_TAG);
	filter_free(&vs->filter_ip6, L3B_SOURCE_FILTER_IP6_TAG);

	if (vs->real_ring.capacity > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_ring.server_indexes),
			sizeof(uint32_t) * vs->real_ring.capacity
		);
	}

	if (vs->real_server_count > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_servers),
			sizeof(struct real_server) * vs->real_server_count
		);
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_object->agent);

	cp_object_fini(cp_object);
	memory_bfree(
		&agent->memory_context,
		object,
		sizeof(struct l3b_virtual_service_object)
	);
}

int
l3b_virtual_service_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	// The session table is deliberately not touched: it survives service
	// updates and is destroyed by its owner once the service is deleted.
	l3b_virtual_service_object_destroy(cp_object);
	return 0;
}

struct l3b_session_table_object *
l3b_virtual_service_session_table(const struct cp_object *cp_object) {
	struct l3b_virtual_service_object *object = container_of(
		cp_object, struct l3b_virtual_service_object, cp_object
	);
	return ADDR_OF(&object->virtual_service.session_table);
}

static struct virtual_service *
l3b_virtual_service_of(struct cp_object *cp_object) {
	return &container_of(
			cp_object, struct l3b_virtual_service_object, cp_object
	)
			->virtual_service;
}

int
l3b_virtual_service_update_ring(
	struct cp_object *cp_object,
	const uint32_t *server_indexes,
	uint32_t server_index_count,
	yanet_error **err
) {
	struct virtual_service *virtual_service =
		l3b_virtual_service_of(cp_object);

	if (server_index_count > virtual_service->real_ring.capacity) {
		yanet_error_add(err, "ring count exceeds capacity");
		return -1;
	}

	for (uint32_t idx = 0; idx < server_index_count; ++idx) {
		if (server_indexes[idx] >= virtual_service->real_server_count) {
			yanet_error_add(err, "invalid real server index");
			return -1;
		}
	}

	// Mark the ring unstable, rewrite the entries, publish the new count
	// and mark it stable again: a reader that acquired the old count
	// retries instead of acting on a mixture of the old and new rings.
	struct real_ring *ring = &virtual_service->real_ring;
	uint32_t sequence = __atomic_load_n(&ring->sequence, __ATOMIC_RELAXED);
	__atomic_store_n(&ring->sequence, sequence + 1, __ATOMIC_RELEASE);

	// Relaxed atomic stores: readers access the entries under the seqlock
	// with matching relaxed atomic loads, keeping the guarded data accesses
	// race-free in the C memory model.
	uint32_t *ring_indexes = ADDR_OF(&ring->server_indexes);
	for (uint32_t idx = 0; idx < server_index_count; ++idx) {
		__atomic_store_n(
			&ring_indexes[idx],
			server_indexes[idx],
			__ATOMIC_RELAXED
		);
	}

	__atomic_store_n(&ring->count, server_index_count, __ATOMIC_RELEASE);
	__atomic_store_n(&ring->sequence, sequence + 2, __ATOMIC_RELEASE);
	return 0;
}

int
l3b_virtual_service_set_real_server_state(
	struct cp_object *cp_object,
	uint32_t real_server_index,
	bool enabled,
	yanet_error **err
) {
	struct virtual_service *virtual_service =
		l3b_virtual_service_of(cp_object);

	if (real_server_index >= virtual_service->real_server_count) {
		yanet_error_add(err, "invalid real server index");
		return -1;
	}

	// Release store pairs with the dataplane's acquire load, so a worker
	// that observes the new state also observes everything published
	// before the change.
	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	__atomic_store_n(
		&real_servers[real_server_index].state,
		enabled ? real_state_enabled : real_state_disabled,
		__ATOMIC_RELEASE
	);
	return 0;
}

struct object *
new_object_l3b_virtual_service() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name,
		L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		sizeof(object->name));
	return object;
}
