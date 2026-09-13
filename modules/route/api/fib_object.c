#include "fib_object.h"

#include "fib.h"

#include "common/container_of.h"
#include "common/memory_address.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/counters/counters.h"

static struct route_fib_object *
route_fib_object_of(struct cp_object *cp_object) {
	return container_of(cp_object, struct route_fib_object, cp_object);
}

static void
route_fib_object_destroy(struct cp_object *cp_object) {
	struct route_fib_object *self = route_fib_object_of(cp_object);
	struct agent *agent = ADDR_OF(&cp_object->agent);

	route_fib_fini(&self->fib, &cp_object->memory_context);
	cp_object_fini(cp_object);

	memory_bfree(
		&agent->memory_context, self, sizeof(struct route_fib_object)
	);
}

struct cp_object *
route_fib_object_new(struct agent *agent, const char *name, yanet_error **err) {
	struct route_fib_object *self =
		(struct route_fib_object *)memory_balloc(
			&agent->memory_context, sizeof(struct route_fib_object)
		);
	if (self == NULL) {
		yanet_error_add(err, "failed to allocate route fib object");
		return NULL;
	}

	if (cp_object_init(
		    &self->cp_object, agent, ROUTE_FIB_OBJECT_TYPE, name, err
	    )) {
		yanet_error_add(err, "failed to init route fib object");
		memory_bfree(
			&agent->memory_context,
			self,
			sizeof(struct route_fib_object)
		);
		return NULL;
	}

	if (route_fib_init(&self->fib, &self->cp_object.memory_context)) {
		yanet_error_add(err, "failed to init route fib object data");
		// A failed table setup never reaches a state its own teardown
		// could walk, and nothing has observed the object yet, so the
		// block is released directly.
		cp_object_fini(&self->cp_object);
		memory_bfree(
			&agent->memory_context,
			self,
			sizeof(struct route_fib_object)
		);
		return NULL;
	}

	return &self->cp_object;
}

int
route_fib_object_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	route_fib_object_destroy(cp_object);
	return 0;
}

int
route_fib_object_add_route(
	struct cp_object *cp_object,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	uint64_t device_index,
	const char *counter_name,
	yanet_error **err
) {
	struct route_fib_object *self = route_fib_object_of(cp_object);

	// The counter registers before the append, so a failed call leaves
	// the table unchanged and at most a counter name nothing references.
	uint64_t counter_id = COUNTER_INVALID;
	if (counter_name != NULL && counter_name[0] != '\0') {
		counter_id = counter_registry_register(
			&cp_object->link_counter_registry, counter_name, 2, err
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

	int route_idx = route_fib_add_route(
		&self->fib,
		&cp_object->memory_context,
		dst_addr,
		src_addr,
		device_index,
		counter_id
	);
	if (route_idx == -1) {
		yanet_error_add(
			err,
			"failed to grow the nexthop table of object '%s'",
			cp_object->name
		);
		return -1;
	}

	return route_idx;
}

int
route_fib_object_add_route_list(
	struct cp_object *cp_object, size_t count, const uint32_t *indexes
) {
	struct route_fib_object *self = route_fib_object_of(cp_object);
	return route_fib_add_route_list(
		&self->fib, &cp_object->memory_context, count, indexes
	);
}

int
route_fib_object_add_prefix_v4(
	struct cp_object *cp_object,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_fib_object *self = route_fib_object_of(cp_object);
	return route_fib_add_prefix_v4(&self->fib, from, to, route_list_index);
}

int
route_fib_object_add_prefix_v6(
	struct cp_object *cp_object,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_fib_object *self = route_fib_object_of(cp_object);
	return route_fib_add_prefix_v6(&self->fib, from, to, route_list_index);
}

const struct route_fib *
route_fib_object_fib(const struct cp_object *cp_object) {
	const struct route_fib_object *self = container_of(
		cp_object, const struct route_fib_object, cp_object
	);
	return &self->fib;
}

const char *
route_fib_object_counter_name(
	const struct cp_object *cp_object, uint64_t counter_id
) {
	const struct counter_registry *registry =
		&cp_object->link_counter_registry;
	if (counter_id == COUNTER_INVALID || counter_id >= registry->count) {
		return "";
	}
	return ADDR_OF(&registry->names)[counter_id].name;
}

uint64_t
route_fib_object_route_count(const struct cp_object *cp_object) {
	return route_fib_object_fib(cp_object)->route_count;
}

uint64_t
route_fib_object_range_count_v4(const struct cp_object *cp_object) {
	return route_fib_range_count_v4(route_fib_object_fib(cp_object));
}

uint64_t
route_fib_object_range_count_v6(const struct cp_object *cp_object) {
	return route_fib_range_count_v6(route_fib_object_fib(cp_object));
}
