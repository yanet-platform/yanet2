#include "l3b_virtual_service_object.h"

#include <errno.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"

#include "l3b_session_table_object.h"

struct l3b_session_timeouts
l3b_session_timeouts_defaults(void) {
	const struct l3b_session_timeouts defaults = {
		.tcp = 60,
		.tcp_syn = 60,
		.tcp_syn_ack = 60,
		.tcp_fin = 60,
		.udp = 60,
		.other = 60,
	};
	return defaults;
}

#include "common/container_of.h"
#include "common/network_mask.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/object/object.h"

/*
 * Source attributes borrow their networks and ranges from service rules.
 */
static inline void
l3b_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct l3b_source_filter_rule *source_rule =
		container_of(rule, struct l3b_source_filter_rule, rule);
	*nets = source_rule->net4s;
}

static inline void
l3b_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct l3b_source_filter_rule *source_rule =
		container_of(rule, struct l3b_source_filter_rule, rule);
	*nets = source_rule->net6s;
}

static inline void
l3b_rule_get_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct l3b_source_filter_rule *source_rule =
		container_of(rule, struct l3b_source_filter_rule, rule);
	ranges->count = source_rule->port_ranges.count;
	ranges->items =
		(const struct filter_u16_span *)source_rule->port_ranges.items;
}

CLASSIFY_NET4_COMPILE(l3b_net4_src, l3b_rule_get_net4_srcs)
CLASSIFY_NET6_COMPILE(l3b_net6_src, l3b_rule_get_net6_srcs)
CLASSIFY_U16_RANGES_COMPILE(
	l3b_port, struct classify_attr_port, l3b_rule_get_port_ranges
)

// Missing-family networks never match; omitted service ports are wildcard.
//
// Excluded rules keep their slots so decoded indices retain rule order.
// Allocation failure returns no projection.
static const struct classifier_rule **
l3b_rule_ptrs_project(
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t rule_count,
	bool family_is_ip4
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(rule_count ? rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		if (family_is_ip4) {
			rule_ptrs[idx] =
				source_filter_rules[idx].net4s.count > 0
					? &source_filter_rules[idx].rule
					: NULL;
		} else {
			rule_ptrs[idx] =
				source_filter_rules[idx].net6s.count > 0
					? &source_filter_rules[idx].rule
					: NULL;
		}
	}

	return rule_ptrs;
}

// Releases one family source classifier; safe on a zeroed one.
static void
l3b_source_classifier_ip4_free(
	struct memory_context *memory_context,
	struct l3b_source_classifier_ip4 *cls
) {
	classify_attr_net4_free(memory_context, &cls->src_attr);
	classify_attr_port_free(memory_context, &cls->port_attr);
	value_table_free(&cls->root_joint);
	vline_free(&cls->rule_map);
	memset(cls, 0, sizeof(*cls));
}

static void
l3b_source_classifier_ip6_free(
	struct memory_context *memory_context,
	struct l3b_source_classifier_ip6 *cls
) {
	classify_attr_net6_free(memory_context, &cls->src_attr);
	classify_attr_port_free(memory_context, &cls->port_attr);
	value_table_free(&cls->root_joint);
	vline_free(&cls->rule_map);
	memset(cls, 0, sizeof(*cls));
}

/*
 * Source classification combines client networks and destination service
 * ports within one address family while preserving rule order.
 *
 * Persistent outputs belong to the service; constructor failure labels
 * release partial construction. Temporary registries and rule mappings
 * are released here on both success and failure.
 */
static int
l3b_module_init_source(
	struct virtual_service *virtual_service,
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t rule_count,
	bool family_is_ip4,
	struct memory_context *memory_context,
	yanet_error **err
) {
	const struct classifier_rule **rule_ptrs = l3b_rule_ptrs_project(
		source_filter_rules, rule_count, family_is_ip4
	);

	struct classifier stage_net = {0};
	struct classifier stage_port = {0};
	struct classifier stage_source = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to allocate source rule ptrs");
		return -1;
	}

	if (family_is_ip4) {
		struct l3b_source_classifier_ip4 *cls =
			&virtual_service->classifier_ip4;
		if (classify_l3b_net4_src_compile(
			    memory_context,
			    "ip4:net4_src",
			    rule_ptrs,
			    rule_count,
			    &cls->src_attr,
			    &stage_net
		    )) {
			goto error;
		}
	} else {
		struct l3b_source_classifier_ip6 *cls =
			&virtual_service->classifier_ip6;
		if (classify_l3b_net6_src_compile(
			    memory_context,
			    "ip6:net6_src",
			    rule_ptrs,
			    rule_count,
			    &cls->src_attr,
			    &stage_net
		    )) {
			goto error;
		}
	}

	if (classify_l3b_port_compile(
		    memory_context,
		    family_is_ip4 ? "ip4:port" : "ip6:port",
		    rule_ptrs,
		    rule_count,
		    family_is_ip4 ? &virtual_service->classifier_ip4.port_attr
				  : &virtual_service->classifier_ip6.port_attr,
		    &stage_port
	    )) {
		goto error;
	}

	if (classify_join(
		    memory_context,
		    family_is_ip4 ? "ip4:root_joint" : "ip6:root_joint",
		    &stage_net,
		    &stage_port,
		    rule_count,
		    family_is_ip4 ? &virtual_service->classifier_ip4.root_joint
				  : &virtual_service->classifier_ip6.root_joint,
		    &stage_source
	    )) {
		goto error;
	}

	if (classify_decode(
		    memory_context,
		    family_is_ip4 ? "ip4:rules" : "ip6:rules",
		    &stage_source,
		    rule_ptrs,
		    rule_count,
		    family_is_ip4 ? &virtual_service->classifier_ip4.rule_map
				  : &virtual_service->classifier_ip6.rule_map
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_net, memory_context, rule_count);
	classifier_fini(&stage_port, memory_context, rule_count);
	classifier_fini(&stage_source, memory_context, rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to compile source classifier");
	}
	return rc;
}

// Rule-less services keep zeroed classifiers and reject source admission.
// Matched Echo requests bypass source admission in service processing.
//
// Masks are validated before classifier construction because prefix-length
// classifiers cannot represent holes within an IPv4 mask or an IPv6 half.
static int
build_source_classifiers(
	struct virtual_service *virtual_service,
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t source_filter_rule_count,
	struct memory_context *memory_context,
	yanet_error **err
) {
	if (source_filter_rule_count == 0) {
		virtual_service->source_filter_rule_count = 0;
		return 0;
	}

	for (uint32_t rule_idx = 0; rule_idx < source_filter_rule_count;
	     ++rule_idx) {
		const struct l3b_source_filter_rule *source_rule =
			&source_filter_rules[rule_idx];
		if (!network_net4_masks_are_valid(
			    source_rule->net4s.items, source_rule->net4s.count
		    )) {
			yanet_error_add(
				err,
				"source filter rule %u has a non-contiguous "
				"IPv4 network mask",
				rule_idx
			);
			return -1;
		}
		if (!network_net6_masks_are_valid(
			    source_rule->net6s.items, source_rule->net6s.count
		    )) {
			yanet_error_add(
				err,
				"source filter rule %u has an IPv6 network "
				"mask that is not bi-contiguous",
				rule_idx
			);
			return -1;
		}
	}

	if (l3b_module_init_source(
		    virtual_service,
		    source_filter_rules,
		    source_filter_rule_count,
		    true,
		    memory_context,
		    err
	    ) ||
	    l3b_module_init_source(
		    virtual_service,
		    source_filter_rules,
		    source_filter_rule_count,
		    false,
		    memory_context,
		    err
	    )) {
		return -1;
	}

	virtual_service->source_filter_rule_count = source_filter_rule_count;
	return 0;
}

static int
register_object_counter(
	struct cp_object *cp_object,
	const char *name,
	uint64_t *counter_id,
	yanet_error **err
) {
	*counter_id = counter_registry_register(
		&cp_object->counter_registry, name, 2, err
	);
	if (*counter_id == COUNTER_INVALID) {
		yanet_error_add(err, "failed to register counter '%s'", name);
		return -1;
	}
	return 0;
}

struct cp_object *
l3b_virtual_service_create(
	const struct l3b_virtual_service_create_config *config,
	yanet_error **err
) {
	if (config->session_table == NULL) {
		yanet_error_add(err, "missing session table");
		return NULL;
	}

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

	// The link packets counter is part of the object's contract: every
	// module link spawns its own per-worker storage for it at
	// execution-context build time.
	if (counter_registry_register(
		    &object->cp_object.link_counter_registry,
		    L3B_LINK_COUNTER_PACKETS,
		    1,
		    err
	    ) == COUNTER_INVALID) {
		yanet_error_add(err, "failed to register link packets counter");
		goto error_object;
	}

	// Every allocation below is attributed to the object's own memory
	// context so the accounting travels with the object.
	struct memory_context *memory_context =
		&object->cp_object.memory_context;
	struct virtual_service *vs = &object->virtual_service;

	// Zero initialization makes empty and partial classifier cleanup safe.
	memset(&vs->classifier_ip6, 0, sizeof(vs->classifier_ip6));
	memset(&vs->classifier_ip4, 0, sizeof(vs->classifier_ip4));
	vs->source_filter_rule_count = 0;

	vs->scheduler_hash_mask = virtual_service->hash_mask;
	vs->scheduler_index_mask = virtual_service->index_mask;
	vs->scheduler_flags = virtual_service->scheduler_flags;
	vs->flags = virtual_service->flags;
	vs->session_timeouts = virtual_service->session_timeouts;
	vs->dscp_flags = virtual_service->dscp_flags;
	const struct l3b_session_timeouts defaults =
		l3b_session_timeouts_defaults();
	if (vs->session_timeouts.tcp == 0) {
		vs->session_timeouts.tcp = defaults.tcp;
	}
	if (vs->session_timeouts.tcp_syn == 0) {
		vs->session_timeouts.tcp_syn = defaults.tcp_syn;
	}
	if (vs->session_timeouts.tcp_syn_ack == 0) {
		vs->session_timeouts.tcp_syn_ack = defaults.tcp_syn_ack;
	}
	if (vs->session_timeouts.tcp_fin == 0) {
		vs->session_timeouts.tcp_fin = defaults.tcp_fin;
	}
	if (vs->session_timeouts.udp == 0) {
		vs->session_timeouts.udp = defaults.udp;
	}
	if (vs->session_timeouts.other == 0) {
		vs->session_timeouts.other = defaults.other;
	}

	vs->real_server_count = virtual_service->real_server_count;

	if (register_object_counter(
		    &object->cp_object,
		    L3B_COUNTER_INCOMING,
		    &vs->counter_incoming,
		    err
	    ) ||
	    register_object_counter(
		    &object->cp_object,
		    L3B_COUNTER_FILTER_REJECTED,
		    &vs->counter_filter_rejected,
		    err
	    ) ||
	    register_object_counter(
		    &object->cp_object,
		    L3B_COUNTER_RING_EMPTY,
		    &vs->counter_ring_empty,
		    err
	    ) ||
	    register_object_counter(
		    &object->cp_object,
		    L3B_COUNTER_REAL_DISABLED,
		    &vs->counter_real_disabled,
		    err
	    ) ||
	    register_object_counter(
		    &object->cp_object,
		    L3B_COUNTER_ICMP_REPLIED,
		    &vs->counter_icmp_replied,
		    err
	    )) {
		goto error_object;
	}

	vs->real_counter_ids = NULL;
	if (vs->real_server_count > 0) {
		uint64_t *real_counter_ids = (uint64_t *)memory_balloc(
			memory_context, sizeof(uint64_t) * vs->real_server_count
		);
		if (real_counter_ids == NULL) {
			yanet_error_add(
				err, "failed to allocate real counters"
			);
			goto error_object;
		}

		SET_OFFSET_OF(&vs->real_counter_ids, real_counter_ids);
		for (uint32_t real_idx = 0; real_idx < vs->real_server_count;
		     ++real_idx) {
			char name[COUNTER_NAME_LEN] = {0};
			snprintf(name, sizeof(name), "real/%" PRIu32, real_idx);
			if (register_object_counter(
				    &object->cp_object,
				    name,
				    &real_counter_ids[real_idx],
				    err
			    )) {
				goto error_real_counters;
			}
		}
	}

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
			goto error_real_counters;
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

	if (build_source_classifiers(
		    vs,
		    virtual_service->source_filter_rules,
		    virtual_service->source_filter_rule_count,
		    memory_context,
		    err
	    )) {
		goto error_ring;
	}

	// The dataplane reaches the table through this pointer; the object
	// itself stays the caller's.
	SET_OFFSET_OF(
		&vs->session_table,
		container_of(
			config->session_table,
			struct l3b_session_table_object,
			cp_object
		)
	);

	return &object->cp_object;

error_ring:
	// Partial classifiers own allocations that accounting teardown cannot
	// free.
	//
	// Explicit release is safe for both zeroed and partially built
	// families.
	l3b_source_classifier_ip4_free(memory_context, &vs->classifier_ip4);
	l3b_source_classifier_ip6_free(memory_context, &vs->classifier_ip6);

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

error_real_counters:
	if (vs->real_counter_ids != NULL) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_counter_ids),
			sizeof(uint64_t) * vs->real_server_count
		);
	}

error_object:
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

	// Classifier release is safe for zeroed families of a rule-less
	// service.
	l3b_source_classifier_ip4_free(memory_context, &vs->classifier_ip4);
	l3b_source_classifier_ip6_free(memory_context, &vs->classifier_ip6);

	if (vs->real_ring.capacity > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_ring.server_indexes),
			sizeof(uint32_t) * vs->real_ring.capacity
		);
	}

	if (vs->real_counter_ids != NULL) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_counter_ids),
			sizeof(uint64_t) * vs->real_server_count
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
	struct agent *agent = ADDR_OF(&cp_object->entity.agent);

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

	struct agent *owner = ADDR_OF(&cp_object->entity.agent);

	// The session table outlives the service and is destroyed through its
	// own handle.
	l3b_virtual_service_object_destroy(cp_object);
	agent_destroy_released(owner);
	return 0;
}

int
l3b_virtual_service_counter_read(
	uint64_t ectx, const char *service, const char *counter, uint64_t *out
) {
	struct config_gen_ectx *gen_ectx =
		(struct config_gen_ectx *)(uintptr_t)ectx;
	struct cp_config_gen *config_gen = ADDR_OF(&gen_ectx->cp_config_gen);

	uint64_t object_idx;
	if (cp_config_gen_lookup_object_index(
		    config_gen,
		    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		    service,
		    &object_idx
	    )) {
		return -1;
	}

	struct object_ectx *object_ectx =
		config_gen_ectx_get_object(gen_ectx, object_idx);
	if (object_ectx == NULL) {
		return -1;
	}

	struct counter_storage *storage =
		ADDR_OF(&object_ectx->counter_storage);
	if (storage == NULL) {
		return -1;
	}

	struct counter_registry *registry = ADDR_OF(&storage->registry);
	uint64_t counter_id = counter_registry_lookup_index(registry, counter);
	if (counter_id == (uint64_t)-1) {
		return -1;
	}

	uint64_t *slot = counter_get_address(counter_id, storage);
	out[0] = slot[0];
	out[1] = slot[1];
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
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name,
		L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		sizeof(object->name));
	return object;
}
