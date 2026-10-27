// Egress backing resolution at execution-context build.
//
// The backing table translates a registry device slot into the physical
// port of the same name, so the write stage never needs the two device
// namespaces to agree on numbering. The scenarios register devices in
// an order deliberately crossed against the port topology and read the
// built table, the emitting handlers' device ids and the worker's
// receive slot back out of the generated context.

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/registry.h"
#include "lib/controlplane/config/registry_sanitize.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/worker/pipeline_round.h"
#include "lib/logging/log.h"
#include "lib/tests/dataplane/fixture.h"

// Registry capacity for the test generation: three devices in the
// crossed order, plus headroom for a hole.
#define BACKING_TEST_REGISTRY_CAPACITY 4

// Wires the controlplane side the execution-context build reads: the
// three memory contexts on the fixture allocator, the dataplane config
// back reference, and one port-backed device type for the registry
// entries to point at.
static int
backing_test_cp_config_init(
	struct dataplane_test_fixture *fixture, struct cp_config *cp_config
) {
	struct dp_config *cfg = &fixture->dp_config;
	memset(cp_config, 0, sizeof(*cp_config));

	memory_context_init(
		&cp_config->memory_context,
		"backing_cp",
		&fixture->block_allocator
	);
	memory_context_init(
		&cp_config->ectx_memory_context,
		"backing_cp_ectx",
		&fixture->block_allocator
	);
	memory_context_init(
		&cp_config->counter_storage_memory_context,
		"backing_cp_counters",
		&fixture->block_allocator
	);
	SET_OFFSET_OF(&cp_config->dp_config, cfg);

	struct dp_device *dp_devices = (struct dp_device *)memory_balloc(
		&cfg->memory_context, sizeof(struct dp_device)
	);
	TEST_ASSERT_NOT_NULL(dp_devices, "dp_devices alloc failed");
	memset(dp_devices, 0, sizeof(struct dp_device));
	SET_OFFSET_OF(&cfg->dp_devices, dp_devices);
	cfg->device_count = 1;

	return TEST_SUCCESS;
}

// Registers one device under the given name, with empty pipeline
// entries and an initialized counter registry, occupying the next free
// registry slot.
static int
backing_test_add_device(
	struct dataplane_test_fixture *fixture,
	struct cp_config_gen *gen,
	struct cp_device *cp_device,
	const char *name
) {
	struct dp_config *cfg = &fixture->dp_config;
	memset(cp_device, 0, sizeof(*cp_device));
	registry_item_init(&cp_device->entity.config_item);
	snprintf(cp_device->type, sizeof(cp_device->type), "%s", "test");
	snprintf(cp_device->name, sizeof(cp_device->name), "%s", name);
	cp_device->dp_device_idx = 0;
	memory_context_init(
		&cp_device->entity.memory_context,
		name,
		&fixture->block_allocator
	);
	if (counter_registry_init(
		    &cp_device->counter_registry, &cfg->memory_context, 1
	    )) {
		return -1;
	}

	struct cp_device_entry *entries[2];
	for (size_t side = 0; side < 2; ++side) {
		entries[side] = (struct cp_device_entry *)memory_balloc(
			&cfg->memory_context, sizeof(struct cp_device_entry)
		);
		TEST_ASSERT_NOT_NULL(
			entries[side], "device entry alloc failed"
		);
		memset(entries[side], 0, sizeof(struct cp_device_entry));
	}
	SET_OFFSET_OF(&cp_device->input_pipelines, entries[0]);
	SET_OFFSET_OF(&cp_device->output_pipelines, entries[1]);

	int ret = registry_insert(
		&gen->device_registry.entity_registry.registry,
		NULL,
		&cp_device->entity.config_item
	);
	registry_sanitize_index(
		&gen->device_registry.entity_registry.registry, NULL
	);
	return ret;
}

// Builds a generation whose registries live on the fixture memory
// context.
static int
backing_test_gen_init(
	struct dp_config *cfg,
	struct cp_config *cp_config,
	struct cp_config_gen *gen
) {
	memset(gen, 0, sizeof(*gen));
	gen->gen = 1;
	SET_OFFSET_OF(&gen->cp_config, cp_config);

	if (registry_init(
		    &cfg->memory_context,
		    NULL,
		    &gen->module_registry.entity_registry.registry,
		    BACKING_TEST_REGISTRY_CAPACITY
	    )) {
		return -1;
	}
	SET_OFFSET_OF(
		&gen->module_registry.entity_registry.memory_context,
		&cfg->memory_context
	);

	if (registry_init(
		    &cfg->memory_context,
		    NULL,
		    &gen->device_registry.entity_registry.registry,
		    BACKING_TEST_REGISTRY_CAPACITY
	    )) {
		return -1;
	}
	SET_OFFSET_OF(
		&gen->device_registry.entity_registry.memory_context,
		&cfg->memory_context
	);

	if (registry_init(
		    &cfg->memory_context,
		    NULL,
		    &gen->object_registry.entity_registry.registry,
		    BACKING_TEST_REGISTRY_CAPACITY
	    )) {
		return -1;
	}
	SET_OFFSET_OF(
		&gen->object_registry.entity_registry.memory_context,
		&cfg->memory_context
	);

	return 0;
}

// Verifies that a generation whose device registration order is
// crossed against the port topology still resolves every egress to the
// port of the same name, marks port-less devices as such, and resolves
// each physical port's receive slot through the reverse backing table.
static int
test_backing_resolves_crossed_registration_order(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "backing_test", 2),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	// The physical topology: port zero answers to "portA", port one to
	// "portB". The registry below deliberately lists them backwards.
	struct dp_port *ports = ADDR_OF(&cfg->dp_topology.devices);
	snprintf(
		ports[0].device_name,
		sizeof(ports[0].device_name),
		"%s",
		"portA"
	);
	snprintf(
		ports[1].device_name,
		sizeof(ports[1].device_name),
		"%s",
		"portB"
	);

	struct cp_config cp_config;
	struct cp_config_gen gen;
	struct cp_device port_b, user_x, port_a;
	TEST_ASSERT_SUCCESS(
		backing_test_cp_config_init(&fixture, &cp_config),
		"cp_config init failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_gen_init(cfg, &cp_config, &gen), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &port_b, "portB"),
		"add portB failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &user_x, "userX"),
		"add userX failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &port_a, "portA"),
		"add portA failed"
	);

	// The worker's single assignment polls physical port zero, "portA".
	struct dp_worker worker;
	memset(&worker, 0, sizeof(worker));
	struct dp_worker_assignment *assignments =
		(struct dp_worker_assignment *)memory_balloc(
			&cfg->memory_context,
			sizeof(struct dp_worker_assignment)
		);
	TEST_ASSERT_NOT_NULL(assignments, "assignment alloc failed");
	*assignments = (struct dp_worker_assignment){
		.device_id = 0,
		.queue_id = 0,
	};
	worker.assignment_count = 1;
	SET_OFFSET_OF(&worker.assignments, assignments);
	struct dp_worker **workers = (struct dp_worker **)memory_balloc(
		&cfg->memory_context, sizeof(struct dp_worker *)
	);
	TEST_ASSERT_NOT_NULL(workers, "worker array alloc failed");
	SET_OFFSET_OF(workers, &worker);
	SET_OFFSET_OF(&cfg->workers, workers);
	cfg->worker_count = 1;

	yanet_error *err = NULL;
	struct config_gen_ectx **ectxs =
		config_gen_ectxs_create(&gen, NULL, 1, &err);
	TEST_ASSERT_NOT_NULL(ectxs, "execution context build failed");

	struct config_gen_ectx *ectx = ADDR_OF(ectxs);
	const uint16_t *backing = ADDR_OF(&ectx->egress_backing);
	TEST_ASSERT_EQUAL(
		backing[0], 1, "portB must back onto physical port one"
	);
	TEST_ASSERT_EQUAL(
		backing[1], UINT16_MAX, "userX has no port counterpart"
	);
	TEST_ASSERT_EQUAL(
		backing[2], 0, "portA must back onto physical port zero"
	);
	const uint16_t *backing_registry = ADDR_OF(&ectx->backing_registry);
	TEST_ASSERT_EQUAL(
		backing_registry[0],
		2,
		"physical port zero's registry slot is portA"
	);
	TEST_ASSERT_EQUAL(
		backing_registry[1],
		0,
		"physical port one's registry slot is portB"
	);

	struct device_ectx **device_ptrs = ADDR_OF(&ectx->device_ptrs);
	for (uint64_t idx = 0; idx < ectx->device_count; ++idx) {
		struct device_ectx *device_ectx = ADDR_OF(device_ptrs + idx);
		if (device_ectx == NULL) {
			continue;
		}
		TEST_ASSERT_EQUAL(
			device_ectx->device_id,
			idx,
			"a device context carries its registry slot"
		);
	}

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that the output-entry terminal resolves the whole entry's
// egress from the entry's device.
//
// A port-backed entry lands every finisher in its port's bucket, and a
// port-less entry drops its finishers on the front's drop list with the
// entry's counters accounting them.
static int
test_terminal_egress_resolves_per_entry(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "backing_test", 2),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	struct dp_port *ports = ADDR_OF(&cfg->dp_topology.devices);
	snprintf(
		ports[0].device_name,
		sizeof(ports[0].device_name),
		"%s",
		"portA"
	);
	snprintf(
		ports[1].device_name,
		sizeof(ports[1].device_name),
		"%s",
		"portB"
	);

	struct cp_config cp_config;
	struct cp_config_gen gen;
	struct cp_device port_b, user_x, port_a;
	TEST_ASSERT_SUCCESS(
		backing_test_cp_config_init(&fixture, &cp_config),
		"cp_config init failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_gen_init(cfg, &cp_config, &gen), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &port_b, "portB"),
		"add portB failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &user_x, "userX"),
		"add userX failed"
	);
	TEST_ASSERT_SUCCESS(
		backing_test_add_device(&fixture, &gen, &port_a, "portA"),
		"add portA failed"
	);

	// The worker's single assignment polls physical port zero, "portA".
	struct dp_worker worker;
	memset(&worker, 0, sizeof(worker));
	struct dp_worker_assignment *assignments =
		(struct dp_worker_assignment *)memory_balloc(
			&cfg->memory_context,
			sizeof(struct dp_worker_assignment)
		);
	TEST_ASSERT_NOT_NULL(assignments, "assignment alloc failed");
	*assignments = (struct dp_worker_assignment){
		.device_id = 0,
		.queue_id = 0,
	};
	worker.assignment_count = 1;
	SET_OFFSET_OF(&worker.assignments, assignments);
	struct dp_worker **workers = (struct dp_worker **)memory_balloc(
		&cfg->memory_context, sizeof(struct dp_worker *)
	);
	TEST_ASSERT_NOT_NULL(workers, "worker array alloc failed");
	SET_OFFSET_OF(workers, &worker);
	SET_OFFSET_OF(&cfg->workers, workers);
	cfg->worker_count = 1;

	yanet_error *err = NULL;
	struct config_gen_ectx **ectxs =
		config_gen_ectxs_create(&gen, NULL, 1, &err);
	TEST_ASSERT_NOT_NULL(ectxs, "execution context build failed");
	struct config_gen_ectx *ectx = ADDR_OF(ectxs);
	ectx->abs_egress_backing = ADDR_OF(&ectx->egress_backing);
	ectx->abs_tx_buckets = ADDR_OF(&ectx->tx_buckets);

	struct device_ectx **device_ptrs = ADDR_OF(&ectx->device_ptrs);

	// A port-backed entry: portA is registry slot two, physical zero.
	struct device_entry_ectx *backed =
		ADDR_OF(&ADDR_OF(device_ptrs + 2)->output_pipelines);
	uint64_t backed_drop[2] = {0, 0};
	backed->counter_packet_drop =
		(struct counter_value_handle *)backed_drop;
	uint64_t backed_tx[2] = {0, 0};
	backed->counter_packet_tx = (struct counter_value_handle *)backed_tx;

	struct packet first, second;
	memset(&first, 0, sizeof(first));
	memset(&second, 0, sizeof(second));
	first.data_len = 100;
	second.data_len = 60;

	struct packet_front front;
	packet_front_init(&front);
	packet_front_output(&front, &first);
	packet_front_output(&front, &second);
	backed_tx[0] = 2;
	backed_tx[1] = 160;

	worker_entry_terminal_egress(ectx, backed, &front);

	TEST_ASSERT_EQUAL(
		0, packet_front_output_count(&front), "the output list drains"
	);
	TEST_ASSERT_EQUAL(
		0, packet_front_drop_count(&front), "nothing drops on a port"
	);
	TEST_ASSERT_EQUAL(
		packet_list_first(ectx->abs_tx_buckets + 0),
		&first,
		"the entry's finishers land in its port's bucket in order"
	);
	TEST_ASSERT_EQUAL(
		packet_list_first(ectx->abs_tx_buckets + 1),
		NULL,
		"nothing lands in the other port's bucket"
	);
	TEST_ASSERT_EQUAL(
		backed_tx[0], 2, "a full port keeps its transmit accounting"
	);
	TEST_ASSERT_EQUAL(backed_drop[0], 0, "a full port drops nothing");

	// A port-less entry: userX is registry slot one, no port answers to
	// its name.
	struct device_entry_ectx *dangling =
		ADDR_OF(&ADDR_OF(device_ptrs + 1)->output_pipelines);
	uint64_t dangling_drop[2] = {0, 0};
	dangling->counter_packet_drop =
		(struct counter_value_handle *)dangling_drop;
	uint64_t dangling_tx[2] = {0, 0};
	dangling->counter_packet_tx =
		(struct counter_value_handle *)dangling_tx;

	struct packet third;
	memset(&third, 0, sizeof(third));
	third.data_len = 40;

	packet_front_init(&front);
	packet_front_output(&front, &third);
	dangling_tx[0] = 1;
	dangling_tx[1] = 40;

	worker_entry_terminal_egress(ectx, dangling, &front);

	TEST_ASSERT_EQUAL(
		1, packet_front_drop_count(&front), "the finisher drops"
	);
	TEST_ASSERT_EQUAL(
		40, packet_front_drop_bytes(&front), "dropped bytes add up"
	);
	TEST_ASSERT_EQUAL(
		dangling_drop[0], 1, "the entry's drop counter saw the packet"
	);
	TEST_ASSERT_EQUAL(
		dangling_drop[1], 40, "the entry's drop counter saw the bytes"
	);
	TEST_ASSERT_EQUAL(
		dangling_tx[0],
		0,
		"the transmit accounting gave the dropped packet back"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	if (test_backing_resolves_crossed_registration_order() !=
	    TEST_SUCCESS) {
		return 1;
	}
	if (test_terminal_egress_resolves_per_entry() != TEST_SUCCESS) {
		return 1;
	}
	printf("backing tests passed\n");
	return 0;
}
