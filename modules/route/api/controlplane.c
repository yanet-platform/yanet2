#include "controlplane.h"

#include "config.h"
#include "fib.h"

#include <stdlib.h>
#include <string.h>

#include "common/container_of.h"

#include "lib/controlplane/agent/agent.h"

struct fib_iter {
	struct route_module_config *config;
	struct route_fib_iter it;
};

int
route_module_config_register_counters(
	struct route_module_config *config, yanet_error **err
) {
	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"route_forwarded_v4", 2, &config->counters_v4.forwarded},
		{"route_forwarded_v6", 2, &config->counters_v6.forwarded},
		{"route_drop_no_route_v4", 2, &config->counters_v4.drop_no_route
		},
		{"route_drop_no_route_v6", 2, &config->counters_v6.drop_no_route
		},
		{"route_drop_ttl_expired_v4",
		 2,
		 &config->counters_v4.drop_ttl_expired},
		{"route_drop_ttl_expired_v6",
		 2,
		 &config->counters_v6.drop_ttl_expired},
		{"route_drop_non_ip", 2, &config->drop_non_ip_counter_id},
		{"route_drop_empty_route_list_v4",
		 2,
		 &config->counters_v4.drop_empty_route_list},
		{"route_drop_empty_route_list_v6",
		 2,
		 &config->counters_v6.drop_empty_route_list},
		{"route_drop_device_unresolved_v4",
		 2,
		 &config->counters_v4.drop_device_unresolved},
		{"route_drop_device_unresolved_v6",
		 2,
		 &config->counters_v6.drop_device_unresolved},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[i].name
			);
			return -1;
		}
		*counters[i].dst = id;
	}

	return 0;
}

static void
route_module_config_destroy(struct cp_module *cp_module);

struct cp_module *
route_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct route_module_config *config =
		(struct route_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct route_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "route", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
		return NULL;
	}

	if (route_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		yanet_error_add(err, "failed to init config data");
		// Frees directly instead of going through the type destructor.
		//
		// A failed configuration-data setup never reaches a state its
		// own teardown could safely walk. No reference beyond the
		// caller's own has been taken, and no registry has observed
		// the module yet, so nothing is lost by freeing the block
		// here.
		cp_module_fini(&config->cp_module);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
		return NULL;
	}

	if (route_module_config_register_counters(config, err)) {
		// Frees directly instead of going through the public free.
		//
		// Registering counters is the last construction step, so
		// configuration data is already fully set up and the type's
		// own destructor can safely walk it. No reference beyond the
		// caller's own has been taken and no registry has observed
		// the module yet, so it is dangling and either path
		// destroys it identically.
		route_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

int
route_module_config_data_init(
	struct route_module_config *config,
	struct memory_context *memory_context
) {
	return route_fib_init(&config->fib, memory_context);
}

void
route_module_config_data_fini(struct route_module_config *config) {
	route_fib_fini(&config->fib, &config->cp_module.memory_context);
}

static void
route_module_config_destroy(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	route_module_config_data_fini(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct route_module_config)
	);
}

int
route_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	route_module_config_destroy(cp_module);
	return 0;
}

int
route_module_config_add_route(
	struct cp_module *cp_module,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	const char *device_name,
	const char *counter_name,
	yanet_error **err
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	uint64_t device_index;
	if (cp_module_link_device(cp_module, device_name, &device_index, err)) {
		return -1;
	}

	uint64_t counter_id = COUNTER_INVALID;
	if (counter_name != NULL && counter_name[0] != '\0') {
		// Per-route counters live in a dedicated "routes" registry so
		// they are separated from the module's predefined counters in
		// the per-worker storages and the counter storage registry.
		struct counter_registry *routes_registry =
			cp_module_counter_registry(
				cp_module,
				"routes",
				&config->routes_registry_idx,
				err
			);
		if (routes_registry == NULL) {
			return -1;
		}

		counter_id = counter_registry_register(
			routes_registry, counter_name, 2, err
		);
		if (counter_id == COUNTER_INVALID) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counter_name
			);
			return -1;
		}
	}

	return route_fib_add_route(
		&config->fib,
		&config->cp_module.memory_context,
		dst_addr,
		src_addr,
		device_index,
		counter_id
	);
}

int
route_module_config_add_route_list(
	struct cp_module *cp_module, size_t count, const uint32_t *indexes
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_fib_add_route_list(
		&config->fib, &config->cp_module.memory_context, count, indexes
	);
}

int
route_module_config_add_prefix_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_fib_add_prefix_v4(
		&config->fib, from, to, route_list_index
	);
}

int
route_module_config_add_prefix_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_fib_add_prefix_v6(
		&config->fib, from, to, route_list_index
	);
}

uint64_t
route_module_config_route_count(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return config->fib.route_count;
}

uint64_t
route_module_config_fib_range_count_v4(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_fib_range_count_v4(&config->fib);
}

uint64_t
route_module_config_fib_range_count_v6(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_fib_range_count_v6(&config->fib);
}

struct fib_iter *
fib_iter_new(struct cp_module *cp_module) {
	struct fib_iter *it = calloc(1, sizeof(*it));
	if (it == NULL) {
		return NULL;
	}
	it->config =
		container_of(cp_module, struct route_module_config, cp_module);
	route_fib_iter_init(&it->it, &it->config->fib);
	return it;
}

void
fib_iter_free(struct fib_iter *it) {
	free(it);
}

bool
fib_iter_next(struct fib_iter *it) {
	return route_fib_iter_next(&it->it);
}

uint8_t
fib_iter_address_family(const struct fib_iter *it) {
	return route_fib_iter_address_family(&it->it);
}

const uint8_t *
fib_iter_prefix_from(const struct fib_iter *it) {
	return route_fib_iter_prefix_from(&it->it);
}

const uint8_t *
fib_iter_prefix_to(const struct fib_iter *it) {
	return route_fib_iter_prefix_to(&it->it);
}

uint64_t
fib_iter_nexthop_count(const struct fib_iter *it) {
	return route_fib_nexthop_count(
		&it->config->fib, route_fib_iter_route_list_id(&it->it)
	);
}

// Resolves the route for the i-th nexthop of the current entry.
static const struct route *
fib_iter_resolve_route(const struct fib_iter *it, uint64_t nexthop_idx) {
	return route_fib_resolve_route(
		&it->config->fib,
		route_fib_iter_route_list_id(&it->it),
		nexthop_idx
	);
}

void
fib_iter_nexthop_dst_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL) {
		*dst = r->dst_addr;
	} else {
		memset(dst, 0, sizeof(*dst));
	}
}

void
fib_iter_nexthop_src_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL) {
		*dst = r->src_addr;
	} else {
		memset(dst, 0, sizeof(*dst));
	}
}

const char *
fib_iter_nexthop_device_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL) {
		return "";
	}

	struct route_module_config *config = it->config;
	struct cp_module_device *devices = ADDR_OF(&config->cp_module.devices);
	if (r->device_id < config->cp_module.device_count) {
		return devices[r->device_id].name;
	}
	return "";
}

const char *
fib_iter_nexthop_counter_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL || r->counter_id == COUNTER_INVALID) {
		return "";
	}

	struct route_module_config *config = it->config;
	if (config->routes_registry_idx >=
	    config->cp_module.runtime_counter_registry_count) {
		return "";
	}
	struct cp_module_counter_registry **registries =
		ADDR_OF(&config->cp_module.runtime_counter_registries);
	struct cp_module_counter_registry *entry =
		ADDR_OF(registries + config->routes_registry_idx);
	struct counter_registry *registry = &entry->registry;
	if (r->counter_id < registry->count) {
		return ADDR_OF(&registry->names)[r->counter_id].name;
	}
	return "";
}
