#include "controlplane.h"

#include "config.h"
#include "fib.h"
#include "fib_object.h"

#include <stdlib.h>
#include <string.h>

#include "common/container_of.h"

#include "lib/controlplane/agent/agent.h"

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
route_module_config_destroy(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct route_module_config)
	);
}

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

	// From here on the module is dangling with nothing but its own
	// resources, so the type destructor is the right teardown for every
	// later failure.
	if (cp_module_link_object(
		    &config->cp_module,
		    ROUTE_FIB_OBJECT_TYPE,
		    name,
		    &config->fib_link_idx,
		    err
	    )) {
		yanet_error_add(err, "failed to link the fib object");
		route_module_config_destroy(&config->cp_module);
		return NULL;
	}

	if (route_module_config_register_counters(config, err)) {
		route_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
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
route_module_config_link_device(
	struct cp_module *cp_module,
	const char *device_name,
	uint32_t *index,
	yanet_error **err
) {
	uint64_t device_index;
	if (cp_module_link_device(cp_module, device_name, &device_index, err)) {
		return -1;
	}
	if (device_index > UINT32_MAX) {
		yanet_error_add(
			err,
			"device table of module '%s' is full",
			cp_module->name
		);
		return -1;
	}
	*index = (uint32_t)device_index;
	return 0;
}

struct route_snapshot {
	struct cp_config *cp_config;
	struct cp_config_gen *config_gen;
	const struct cp_module *module;
	// The table object, NULL while the generation holds none.
	const struct cp_object *object;
};

struct route_snapshot *
route_snapshot_open(struct agent *agent, const char *name, yanet_error **err) {
	struct route_snapshot *snapshot = calloc(1, sizeof(*snapshot));
	if (snapshot == NULL) {
		yanet_error_add(err, "failed to allocate the snapshot");
		return NULL;
	}

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *config_gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	struct cp_module *cp_module =
		cp_config_gen_lookup_module(config_gen, "route", name);
	if (cp_module == NULL) {
		cp_config_lock(cp_config);
		cp_config_gen_release(cp_config, config_gen);
		cp_config_unlock(cp_config);
		free(snapshot);
		yanet_error_add_kind(
			err,
			YANET_ERROR_NOT_FOUND,
			"route config '%s' is not snapshot",
			name
		);
		return NULL;
	}

	snapshot->cp_config = cp_config;
	snapshot->config_gen = config_gen;
	snapshot->module = cp_module;
	snapshot->object = cp_config_gen_lookup_object(
		config_gen, ROUTE_FIB_OBJECT_TYPE, name
	);
	return snapshot;
}

void
route_snapshot_close(struct route_snapshot *snapshot) {
	if (snapshot == NULL) {
		return;
	}
	cp_config_lock(snapshot->cp_config);
	cp_config_gen_release(snapshot->cp_config, snapshot->config_gen);
	cp_config_unlock(snapshot->cp_config);
	free(snapshot);
}

bool
route_snapshot_has_fib(const struct route_snapshot *snapshot) {
	return snapshot->object != NULL;
}

uint64_t
route_snapshot_device_count(const struct route_snapshot *snapshot) {
	return snapshot->module->device_count;
}

const char *
route_snapshot_device_name(
	const struct route_snapshot *snapshot, uint64_t index
) {
	if (index >= snapshot->module->device_count) {
		return "";
	}
	const struct cp_module_device *devices =
		ADDR_OF(&snapshot->module->devices);
	return devices[index].name;
}

struct fib_iter {
	const struct cp_object *object;
	// The module that resolves device names, NULL when unknown.
	const struct cp_module *module;
	struct route_fib_iter it;
};

struct fib_iter *
route_snapshot_fib_iter(struct route_snapshot *snapshot, yanet_error **err) {
	if (snapshot->object == NULL) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_NOT_FOUND,
			"route config '%s' has no table snapshot",
			snapshot->module->name
		);
		return NULL;
	}

	struct fib_iter *it = calloc(1, sizeof(*it));
	if (it == NULL) {
		yanet_error_add(err, "failed to allocate the table walk");
		return NULL;
	}
	it->object = snapshot->object;
	it->module = snapshot->module;
	route_fib_iter_init(&it->it, route_fib_object_fib(snapshot->object));
	return it;
}

struct fib_iter *
fib_iter_new(struct cp_object *cp_object) {
	struct fib_iter *it = calloc(1, sizeof(*it));
	if (it == NULL) {
		return NULL;
	}
	it->object = cp_object;
	route_fib_iter_init(&it->it, route_fib_object_fib(cp_object));
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
		it->it.fib, route_fib_iter_route_list_id(&it->it)
	);
}

// Resolves the route for the i-th nexthop of the current entry.
static const struct route *
fib_iter_resolve_route(const struct fib_iter *it, uint64_t nexthop_idx) {
	return route_fib_resolve_route(
		it->it.fib, route_fib_iter_route_list_id(&it->it), nexthop_idx
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
	if (r == NULL || it->module == NULL ||
	    r->device_id >= it->module->device_count) {
		return "";
	}
	const struct cp_module_device *devices = ADDR_OF(&it->module->devices);
	return devices[r->device_id].name;
}

const char *
fib_iter_nexthop_counter_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL) {
		return "";
	}
	return route_fib_object_counter_name(it->object, r->counter_id);
}
