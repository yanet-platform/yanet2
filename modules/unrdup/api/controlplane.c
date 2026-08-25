#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

#include "config.h"
#include "controlplane.h"

#include "common/container_of.h"
#include "common/memory_address.h"
#include "lib/errors/errors.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/filter/compiler.h"
#include "lib/filter/rule.h"

FILTER_COMPILER_DECLARE(UNRDUP_FILTER4_TAG, net4_src, port_src);
FILTER_COMPILER_DECLARE(UNRDUP_FILTER6_TAG, net6_src, port_src);

static void
unrdup_services_free(
	struct memory_context *memory_context,
	struct unrdup_service *services,
	uint64_t service_count
) {
	for (uint64_t idx = 0; idx < service_count; ++idx) {
		struct unrdup_peer *peers = ADDR_OF(&services[idx].peers);
		if (peers != NULL) {
			memory_bfree(
				memory_context,
				peers,
				sizeof(struct unrdup_peer) *
					services[idx].peer_count
			);
		}
	}

	memory_bfree(
		memory_context,
		services,
		sizeof(struct unrdup_service) * service_count
	);
}

static void
unrdup_filter_free(
	struct memory_context *memory_context,
	struct filter *filter,
	const struct filter_compiler *compiler
) {
	if (filter == NULL) {
		return;
	}

	filter_free(filter, compiler);
	memory_bfree(memory_context, filter, sizeof(struct filter));
}

static void
unrdup_endpoints_free(
	struct memory_context *memory_context,
	struct unrdup_endpoint *endpoints,
	uint64_t endpoint_count
) {
	if (endpoints == NULL) {
		return;
	}

	memory_bfree(
		memory_context,
		endpoints,
		sizeof(struct unrdup_endpoint) * endpoint_count
	);
}

static void
unrdup_module_config_data_init(struct unrdup_module_config *config) {
	memset(&config->source4, 0, sizeof(config->source4));
	memset(&config->source6, 0, sizeof(config->source6));

	SET_OFFSET_OF(&config->services, NULL);
	config->service_count = 0;

	SET_OFFSET_OF(&config->filter4, NULL);
	SET_OFFSET_OF(&config->filter6, NULL);

	SET_OFFSET_OF(&config->endpoints4, NULL);
	config->endpoint4_count = 0;
	SET_OFFSET_OF(&config->endpoints6, NULL);
	config->endpoint6_count = 0;
}

static uint32_t
unrdup_proto_bit(uint8_t proto) {
	if (proto == IPPROTO_TCP) {
		return UNRDUP_PROTO_TCP_BIT;
	}
	if (proto == IPPROTO_UDP) {
		return UNRDUP_PROTO_UDP_BIT;
	}

	return 0;
}

// One slot per compiled rule: the rule and everything it points at live
// together so the whole batch is a single allocation.
struct unrdup_rule_slot {
	struct filter_rule rule;
	struct net4 net4;
	struct net6 net6;
	struct filter_port_range port;
	struct unrdup_endpoint endpoint;
};

// What one family contributes to a published configuration.
struct unrdup_family_view {
	struct filter *filter;
	struct unrdup_endpoint *endpoints;
	uint64_t endpoint_count;
};

static void
unrdup_family_view_free(
	struct memory_context *memory_context,
	struct unrdup_family_view *view,
	const struct filter_compiler *compiler
) {
	unrdup_filter_free(memory_context, view->filter, compiler);
	unrdup_endpoints_free(
		memory_context, view->endpoints, view->endpoint_count
	);
	memset(view, 0, sizeof(*view));
}

static void
unrdup_family_clear(
	struct memory_context *memory_context,
	struct filter **filter,
	struct unrdup_endpoint **endpoints,
	uint64_t *endpoint_count,
	const struct filter_compiler *compiler
) {
	struct unrdup_family_view view = {
		.filter = ADDR_OF(filter),
		.endpoints = ADDR_OF(endpoints),
		.endpoint_count = *endpoint_count,
	};

	unrdup_family_view_free(memory_context, &view, compiler);

	SET_OFFSET_OF(filter, NULL);
	SET_OFFSET_OF(endpoints, NULL);
	*endpoint_count = 0;
}

// Endpoints of one VIP and port share a rule, so a service reachable over both
// transports on one port stays a single match carrying both bits.
static void
unrdup_slots_add(
	struct unrdup_rule_slot *slots,
	uint64_t *count,
	const struct unrdup_service_config *service_config,
	uint64_t service_idx,
	const struct unrdup_port_config *port_config,
	enum ip_family family
) {
	uint32_t bit = unrdup_proto_bit(port_config->proto);
	if (bit == 0) {
		return;
	}

	int is4 = family == ip_family_ip4;
	size_t addr_len = is4 ? NET4_LEN : NET6_LEN;

	for (uint64_t idx = 0; idx < *count; ++idx) {
		struct unrdup_rule_slot *slot = slots + idx;
		const uint8_t *addr = is4 ? slot->net4.addr : slot->net6.addr;

		if (slot->port.from == port_config->port &&
		    memcmp(addr, &service_config->vip, addr_len) == 0) {
			slot->endpoint.proto_mask |= bit;
			return;
		}
	}

	struct unrdup_rule_slot *slot = slots + *count;

	slot->port.from = port_config->port;
	slot->port.to = port_config->port;
	slot->rule.transport.src_count = 1;
	slot->rule.transport.srcs = &slot->port;

	if (is4) {
		memcpy(slot->net4.addr, service_config->vip.v4.bytes, NET4_LEN);
		memset(slot->net4.mask, 0xff, NET4_LEN);
		slot->rule.net4.src_count = 1;
		slot->rule.net4.srcs = &slot->net4;
	} else {
		memcpy(slot->net6.addr, service_config->vip.v6.bytes, NET6_LEN);
		memset(slot->net6.mask, 0xff, NET6_LEN);
		slot->rule.net6.src_count = 1;
		slot->rule.net6.srcs = &slot->net6;
	}

	slot->endpoint.service_idx = service_idx;
	slot->endpoint.proto_mask = bit;

	*count += 1;
}

void
unrdup_module_config_data_fini(struct unrdup_module_config *config) {
	struct memory_context *memory_context =
		&config->cp_module.memory_context;

	unrdup_family_clear(
		memory_context,
		&config->filter4,
		&config->endpoints4,
		&config->endpoint4_count,
		UNRDUP_FILTER4_TAG
	);
	unrdup_family_clear(
		memory_context,
		&config->filter6,
		&config->endpoints6,
		&config->endpoint6_count,
		UNRDUP_FILTER6_TAG
	);

	struct unrdup_service *services = ADDR_OF(&config->services);
	if (services != NULL) {
		unrdup_services_free(
			memory_context, services, config->service_count
		);
		SET_OFFSET_OF(&config->services, NULL);
		config->service_count = 0;
	}
}

struct cp_module *
unrdup_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct unrdup_module_config *config =
		(struct unrdup_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct unrdup_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "unrdup", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct unrdup_module_config)
		);
		return NULL;
	}

	unrdup_module_config_data_init(config);

	if (unrdup_module_config_register_counters(&config->cp_module, err)) {
		unrdup_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

int
unrdup_module_config_register_counters(
	struct cp_module *cp_module, yanet_error **err
) {
	struct unrdup_module_config *config =
		container_of(cp_module, struct unrdup_module_config, cp_module);

	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"unrdup_redistributed", 2, &config->redistributed_counter_id},
		{"unrdup_tunneled_received",
		 2,
		 &config->tunneled_received_counter_id},
		{"unrdup_clones_sent", 2, &config->clones_sent_counter_id},
		{"unrdup_clone_failed", 1, &config->clone_failed_counter_id},
		{"unrdup_encap_failed", 1, &config->encap_failed_counter_id},
		{"unrdup_peer_no_source", 1, &config->peer_no_source_counter_id
		},
		{"unrdup_unserved", 1, &config->unserved_counter_id},
		{"unrdup_misaddressed", 1, &config->misaddressed_counter_id},
		{"unrdup_malformed", 1, &config->malformed_counter_id},
	};

	for (size_t idx = 0; idx < sizeof(counters) / sizeof(counters[0]);
	     ++idx) {
		uint64_t id = counter_registry_register(
			&cp_module->counter_registry,
			counters[idx].name,
			counters[idx].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[idx].name
			);
			return -1;
		}

		*counters[idx].dst = id;
	}

	return 0;
}

void
unrdup_module_config_destroy(struct cp_module *cp_module) {
	struct unrdup_module_config *config =
		container_of(cp_module, struct unrdup_module_config, cp_module);

	unrdup_module_config_data_fini(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct unrdup_module_config)
	);
}

int
unrdup_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	unrdup_module_config_destroy(cp_module);
	return 0;
}

int
unrdup_module_config_set_source(
	struct cp_module *cp_module,
	enum ip_family family,
	const uint8_t *addr,
	const uint8_t *mask,
	yanet_error **err
) {
	struct unrdup_module_config *config =
		container_of(cp_module, struct unrdup_module_config, cp_module);

	if (family == ip_family_ip4) {
		memcpy(config->source4.v4.addr, addr, NET4_LEN);
		memcpy(config->source4.v4.mask, mask, NET4_LEN);
		return 0;
	}

	if (family == ip_family_ip6) {
		memcpy(config->source6.v6.addr, addr, NET6_LEN);
		memcpy(config->source6.v6.mask, mask, NET6_LEN);
		return 0;
	}

	yanet_error_add(err, "unknown address family %d", family);
	return -1;
}

static int
unrdup_service_init(
	struct memory_context *memory_context,
	struct unrdup_service *service,
	const struct unrdup_service_config *service_config
) {
	if (service_config->peer_count == 0) {
		return 0;
	}

	size_t size = sizeof(struct unrdup_peer) * service_config->peer_count;

	struct unrdup_peer *peers =
		(struct unrdup_peer *)memory_balloc(memory_context, size);
	if (peers == NULL) {
		return -1;
	}

	for (uint64_t idx = 0; idx < service_config->peer_count; ++idx) {
		peers[idx].addr = service_config->peers[idx].addr;
		peers[idx].family = service_config->peers[idx].family;
	}

	SET_OFFSET_OF(&service->peers, peers);
	service->peer_count = service_config->peer_count;

	return 0;
}

static int
unrdup_family_publish(
	struct cp_module *cp_module,
	const struct unrdup_service_config *service_configs,
	uint64_t service_count,
	enum ip_family family,
	const struct filter_compiler *compiler,
	const char *name,
	struct unrdup_family_view *view,
	yanet_error **err
) {
	struct memory_context *memory_context = &cp_module->memory_context;

	memset(view, 0, sizeof(*view));

	uint64_t capacity = 0;
	for (uint64_t idx = 0; idx < service_count; ++idx) {
		if (service_configs[idx].family == family) {
			capacity += service_configs[idx].port_count;
		}
	}

	if (capacity == 0) {
		return 0;
	}

	struct unrdup_rule_slot *slots =
		calloc(capacity, sizeof(struct unrdup_rule_slot));
	const struct filter_rule **rule_ptrs =
		calloc(capacity, sizeof(struct filter_rule *));
	if (slots == NULL || rule_ptrs == NULL) {
		yanet_error_add(err, "failed to allocate %s rules", name);
		goto error_slots;
	}

	uint64_t count = 0;
	for (uint64_t idx = 0; idx < service_count; ++idx) {
		const struct unrdup_service_config *service_config =
			service_configs + idx;
		if (service_config->family != family) {
			continue;
		}

		for (uint64_t port_idx = 0;
		     port_idx < service_config->port_count;
		     ++port_idx) {
			unrdup_slots_add(
				slots,
				&count,
				service_config,
				idx,
				service_config->ports + port_idx,
				family
			);
		}
	}

	if (count == 0) {
		free(rule_ptrs);
		free(slots);
		return 0;
	}

	size_t size = sizeof(struct unrdup_endpoint) * count;
	struct unrdup_endpoint *endpoints =
		(struct unrdup_endpoint *)memory_balloc(memory_context, size);
	if (endpoints == NULL) {
		yanet_error_add(err, "failed to allocate %s endpoints", name);
		goto error_slots;
	}

	for (uint64_t idx = 0; idx < count; ++idx) {
		endpoints[idx] = slots[idx].endpoint;
		rule_ptrs[idx] = &slots[idx].rule;
	}

	// The filter keeps relative pointers into itself, so it is built where
	// it will stay and published by swapping the pointer.
	struct filter *filter = (struct filter *)memory_balloc(
		memory_context, sizeof(struct filter)
	);
	if (filter == NULL) {
		yanet_error_add(err, "failed to allocate %s", name);
		goto error_endpoints;
	}

	memset(filter, 0, sizeof(struct filter));

	if (filter_init(
		    filter,
		    compiler,
		    rule_ptrs,
		    (uint32_t)count,
		    memory_context,
		    name,
		    err
	    )) {
		yanet_error_add(err, "failed to init %s", name);
		memory_bfree(memory_context, filter, sizeof(struct filter));
		goto error_endpoints;
	}

	view->filter = filter;
	view->endpoints = endpoints;
	view->endpoint_count = count;

	free(rule_ptrs);
	free(slots);

	return 0;

error_endpoints:
	memory_bfree(memory_context, endpoints, size);
error_slots:
	free(rule_ptrs);
	free(slots);
	return -1;
}

int
unrdup_module_config_update_services(
	struct cp_module *cp_module,
	const struct unrdup_service_config *service_configs,
	uint64_t service_count,
	yanet_error **err
) {
	struct unrdup_module_config *config =
		container_of(cp_module, struct unrdup_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;

	if (service_count == 0) {
		unrdup_module_config_data_fini(config);
		return 0;
	}

	size_t size = sizeof(struct unrdup_service) * service_count;

	struct unrdup_service *services =
		(struct unrdup_service *)memory_balloc(memory_context, size);
	if (services == NULL) {
		yanet_error_add(err, "failed to allocate services");
		return -1;
	}

	memset(services, 0, size);

	for (uint64_t idx = 0; idx < service_count; ++idx) {
		if (unrdup_service_init(
			    memory_context,
			    services + idx,
			    service_configs + idx
		    )) {
			yanet_error_add(
				err, "failed to allocate service %lu", idx
			);
			unrdup_services_free(
				memory_context, services, service_count
			);
			return -1;
		}
	}

	struct unrdup_family_view view4;
	struct unrdup_family_view view6;

	if (unrdup_family_publish(
		    cp_module,
		    service_configs,
		    service_count,
		    ip_family_ip4,
		    UNRDUP_FILTER4_TAG,
		    "unrdup_filter4",
		    &view4,
		    err
	    )) {
		unrdup_services_free(memory_context, services, service_count);
		return -1;
	}

	if (unrdup_family_publish(
		    cp_module,
		    service_configs,
		    service_count,
		    ip_family_ip6,
		    UNRDUP_FILTER6_TAG,
		    "unrdup_filter6",
		    &view6,
		    err
	    )) {
		unrdup_family_view_free(
			memory_context, &view4, UNRDUP_FILTER4_TAG
		);
		unrdup_services_free(memory_context, services, service_count);
		return -1;
	}

	unrdup_module_config_data_fini(config);

	SET_OFFSET_OF(&config->services, services);
	config->service_count = service_count;

	SET_OFFSET_OF(&config->filter4, view4.filter);
	SET_OFFSET_OF(&config->endpoints4, view4.endpoints);
	config->endpoint4_count = view4.endpoint_count;

	SET_OFFSET_OF(&config->filter6, view6.filter);
	SET_OFFSET_OF(&config->endpoints6, view6.endpoints);
	config->endpoint6_count = view6.endpoint_count;

	return 0;
}
