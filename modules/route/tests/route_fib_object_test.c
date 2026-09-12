// Pins the FIB object's table and lifecycle.
//
// A table built through the object walks back the ranges and nexthops it
// was given with the device index and counter name intact, the object is
// refused destruction while a generation references it and destroyed once
// the generation drops it, and nothing of it stays in the arena afterwards.

#include "api/agent.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "modules/route/api/fib.h"
#include "modules/route/api/fib_object.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ROUTE_FIB_OBJECT_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

static const struct ether_addr dst_a = {
	.addr = {0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
};
static const struct ether_addr dst_b = {
	.addr = {0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}
};
static const struct ether_addr src = {
	.addr = {0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
};

static int
run_table_walk_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "fib-walk", ROUTE_FIB_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *obj = route_fib_object_new(agent, "fib0", &err);
	TEST_ASSERT_NOT_NULL(
		obj,
		"route_fib_object_new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT(
		!strcmp(obj->type, ROUTE_FIB_OBJECT_TYPE),
		"object type must be the FIB type, got '%s'",
		obj->type
	);

	int route_a = route_fib_object_add_route(
		obj, dst_a, src, 1, "nexthop_a", &err
	);
	TEST_ASSERT(route_a == 0, "first nexthop must take index 0");
	int route_b =
		route_fib_object_add_route(obj, dst_b, src, 2, NULL, &err);
	TEST_ASSERT(route_b == 1, "second nexthop must take index 1");

	uint32_t both[] = {(uint32_t)route_a, (uint32_t)route_b};
	int ecmp = route_fib_object_add_route_list(obj, 2, both);
	TEST_ASSERT(ecmp == 0, "first route list must take index 0");
	uint32_t only_b[] = {(uint32_t)route_b};
	int single = route_fib_object_add_route_list(obj, 1, only_b);
	TEST_ASSERT(single == 1, "second route list must take index 1");

	TEST_ASSERT_SUCCESS(
		route_fib_object_add_prefix_v4(
			obj,
			(uint8_t[4]){10, 0, 0, 0},
			(uint8_t[4]){10, 0, 0, 255},
			(uint32_t)ecmp
		),
		"add_prefix_v4 failed"
	);
	TEST_ASSERT_SUCCESS(
		route_fib_object_add_prefix_v6(
			obj,
			(uint8_t[16]){0xfd, 0x00, [15] = 0},
			(uint8_t[16]){0xfd, 0x00, [2 ... 15] = 0xff},
			(uint32_t)single
		),
		"add_prefix_v6 failed"
	);

	const struct route_fib *fib = route_fib_object_fib(obj);
	TEST_ASSERT_EQUAL(
		(long)route_fib_range_count_v4(fib),
		1L,
		"one IPv4 range was inserted"
	);
	TEST_ASSERT_EQUAL(
		(long)route_fib_range_count_v6(fib),
		1L,
		"one IPv6 range was inserted"
	);
	TEST_ASSERT_EQUAL(
		(long)fib->route_count, 2L, "two nexthops were added"
	);

	// The walk yields the IPv4 range first, resolving to the ECMP list,
	// then the IPv6 range resolving to the single nexthop.
	struct route_fib_iter it;
	route_fib_iter_init(&it, fib);

	TEST_ASSERT(route_fib_iter_next(&it), "walk must yield the IPv4 range");
	TEST_ASSERT_EQUAL(
		(long)route_fib_iter_address_family(&it),
		4L,
		"first range must be IPv4"
	);
	TEST_ASSERT(
		!memcmp(route_fib_iter_prefix_from(&it),
			(uint8_t[4]){10, 0, 0, 0},
			4),
		"IPv4 range start must be the inserted one"
	);
	uint32_t list_id = route_fib_iter_route_list_id(&it);
	TEST_ASSERT_EQUAL(
		(long)route_fib_nexthop_count(fib, list_id),
		2L,
		"IPv4 range must select the ECMP list"
	);
	const struct route *first = route_fib_resolve_route(fib, list_id, 0);
	TEST_ASSERT_NOT_NULL(first, "first nexthop must resolve");
	TEST_ASSERT(
		!memcmp(&first->dst_addr, &dst_a, sizeof(dst_a)),
		"first nexthop must carry its destination MAC"
	);
	TEST_ASSERT_EQUAL(
		(long)first->device_id,
		1L,
		"first nexthop must keep the device index it was given"
	);
	TEST_ASSERT(
		!strcmp(route_fib_object_counter_name(obj, first->counter_id),
			"nexthop_a"),
		"first nexthop must resolve its counter name"
	);
	const struct route *second = route_fib_resolve_route(fib, list_id, 1);
	TEST_ASSERT_NOT_NULL(second, "second nexthop must resolve");
	TEST_ASSERT(
		second->counter_id == COUNTER_INVALID,
		"an uncounted nexthop must carry no counter"
	);
	TEST_ASSERT(
		!strcmp(route_fib_object_counter_name(obj, second->counter_id),
			""),
		"an uncounted nexthop must render an empty counter name"
	);
	TEST_ASSERT_NULL(
		route_fib_resolve_route(fib, list_id, 2),
		"a nexthop index past the list must not resolve"
	);
	TEST_ASSERT_NULL(
		route_fib_resolve_route(fib, 7, 0),
		"a route list the table does not hold must not resolve"
	);
	TEST_ASSERT_EQUAL(
		(long)route_fib_nexthop_count(fib, 7),
		0L,
		"a route list the table does not hold must count no nexthops"
	);
	TEST_ASSERT(
		!strcmp(route_fib_object_counter_name(obj, 5), ""),
		"a counter id past the registry must render an empty name"
	);

	TEST_ASSERT(route_fib_iter_next(&it), "walk must yield the IPv6 range");
	TEST_ASSERT_EQUAL(
		(long)route_fib_iter_address_family(&it),
		6L,
		"second range must be IPv6"
	);
	TEST_ASSERT_EQUAL(
		(long)route_fib_nexthop_count(
			fib, route_fib_iter_route_list_id(&it)
		),
		1L,
		"IPv6 range must select the single nexthop"
	);
	TEST_ASSERT(
		!route_fib_iter_next(&it), "walk must end after both trees"
	);

	// The counter registered for the nexthop lives in the link registry,
	// so each linking module gets its own per-worker storage for it.
	TEST_ASSERT_EQUAL(
		(long)obj->link_counter_registry.count,
		1L,
		"the nexthop counter must register in the link registry"
	);
	TEST_ASSERT_EQUAL(
		(long)obj->counter_registry.count,
		0L,
		"the object's own registry must stay empty"
	);

	TEST_ASSERT_SUCCESS(
		route_fib_object_free(obj, &err),
		"free of a dangling object failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

static int
run_publish_lifecycle_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "fib-publish", ROUTE_FIB_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *obj = route_fib_object_new(agent, "fib0", &err);
	TEST_ASSERT_NOT_NULL(obj, "route_fib_object_new failed");

	struct cp_object *objects[] = {obj};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 1, objects, &err),
		"agent_update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(
			gen, ROUTE_FIB_OBJECT_TYPE, "fib0", &idx
		),
		"the published object must be found by type and name"
	);
	TEST_ASSERT(
		cp_config_gen_get_object(gen, idx) == obj,
		"the generation must hold the published object"
	);

	// The live generation references the object, so the free is refused
	// and the object stays intact for a later retry.
	errno = 0;
	TEST_ASSERT(
		route_fib_object_free(obj, &err) == -1 && errno == EAGAIN,
		"free must be refused while a generation references the object"
	);
	yanet_error_reset(&err);

	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, ROUTE_FIB_OBJECT_TYPE, "fib0", &err),
		"agent_delete_object failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		route_fib_object_free(obj, &err),
		"free after the generation dropped the object failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *modules[] = {"route"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {ROUTE_FIB_OBJECT_TYPE};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = modules,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int res = run_table_walk_test(shm);
	if (res == TEST_SUCCESS) {
		res = run_publish_lifecycle_test(shm);
	}
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
