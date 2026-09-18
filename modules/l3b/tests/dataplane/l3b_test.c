// Verifies that a virtual service published through one mapping of the
// file-backed shared-memory segment is fully usable from a second, separate
// mapping: the per-real counter ids must resolve inside the reading mapping,
// never as a control-plane address.
//
// The test follows the production attach sequence over the first mapping
// (segment initialisation, readiness, agent attach), creates a service with
// one real and installs its scheduler ring, then maps the same file a second
// time at a different address and drops the first mapping. Both packets then
// travel the dataplane entry from the second mapping alone: the first is
// dispatched by the scheduler and pins the flow, the second arrives with an
// emptied ring and can only be counted through the live session. The counter
// ids must agree across both mappings and both increments must land in the
// file-backed storage.

#include <fcntl.h>
#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#include <rte_byteorder.h>
#include <rte_eal.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "api/agent.h"
#include "api/counter.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/strutils.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "modules/l3b/dataplane/process.h"

#define DP_ZONE_SIZE (1 << 20)
#define CP_ZONE_SIZE (24 << 20)
#define SEGMENT_SIZE (DP_ZONE_SIZE + CP_ZONE_SIZE)
// Keep the arena request below the allocator's 16 MiB pool boundary: the
// sanitizer build pads every request into the next power-of-two size class,
// and a 16 MiB request would then need a 32 MiB block that this segment can
// never hold.
#define AGENT_MEMORY (8 << 20)

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

#define SERVICE_PORT 80
#define CLIENT_PORT 12345

// One second past boot: the session the first packet pins must still be live
// for the second one.
#define WORKER_TIME_NS 1000000000ull

static const uint8_t client4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t vip4[NET4_LEN] = {192, 168, 1, 1};
static const uint8_t real4[NET4_LEN] = {172, 16, 0, 10};
static const uint8_t source4[NET4_LEN] = {192, 0, 2, 1};
static const uint8_t source_mask4[NET4_LEN] = {255, 255, 255, 255};

static struct net4 any4;
static struct net6 any6;
static struct filter_port_range service_ports[1];
static struct l3b_source_filter_rule source_rule;
static struct l3b_real_server real_server;
static struct l3b_virtual_service descriptor;

static struct dp_worker worker;
static int segment_fd = -1;
static char *first_mapping;
static char *second_mapping;
static struct agent *agent;
static struct cp_object *service_object;
static struct counter_storage *service_counters;
static struct virtual_service *published_service;
static uintptr_t service_offset;
static uintptr_t counters_offset;
static uintptr_t object_offset;

static void
log_error(yanet_error **err, const char *what) {
	char *message = yanet_error_format(*err);
	LOG(ERROR, "%s: %s", what, message != NULL ? message : "unknown");
	free(message);
}

static int
setup_dpdk(void) {
	char *argv[] = {
		"l3b_test",
		"--no-huge",
		"--no-pci",
		"--iova-mode=va",
		"--file-prefix",
		"l3b_shm_test",
		NULL,
	};

	if (rte_eal_init(6, argv) < 0) {
		return -1;
	}

	memset(&worker, 0, sizeof(worker));
	worker.idx = 0;
	worker.current_time = WORKER_TIME_NS;

	return 0;
}

static int
setup_segment(void) {
	char path[] = "/tmp/l3b_shm_testXXXXXX";
	segment_fd = mkstemp(path);
	if (segment_fd == -1) {
		return -1;
	}
	unlink(path);

	if (ftruncate(segment_fd, SEGMENT_SIZE) == -1) {
		close(segment_fd);
		return -1;
	}

	first_mapping = (char *)mmap(
		NULL,
		SEGMENT_SIZE,
		PROT_READ | PROT_WRITE,
		MAP_SHARED,
		segment_fd,
		0
	);
	if (first_mapping == MAP_FAILED) {
		first_mapping = NULL;
		close(segment_fd);
		return -1;
	}

	return 0;
}

// Register the object types the service and its session table are published
// under; the dataplane normally fills this array from its startup config.
static int
register_object_types(struct dp_config *dp_config) {
	static const char *const types[] = {
		L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		L3B_SESSION_TABLE_OBJECT_TYPE,
	};
	size_t count = sizeof(types) / sizeof(types[0]);

	struct dp_object *dp_objects = (struct dp_object *)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_object) * count
	);
	if (dp_objects == NULL) {
		return -1;
	}

	for (size_t idx = 0; idx < count; ++idx) {
		strtcpy(dp_objects[idx].name,
			types[idx],
			sizeof(dp_objects[idx].name));
	}
	SET_OFFSET_OF(&dp_config->dp_objects, dp_objects);
	dp_config->object_count = count;

	return 0;
}

static int
publish_service(void) {
	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	if (dp_storage_init(
		    0,
		    0,
		    first_mapping,
		    DP_ZONE_SIZE,
		    CP_ZONE_SIZE,
		    &dp_config,
		    &cp_config
	    )) {
		LOG(ERROR, "failed to initialise the segment");
		return -1;
	}

	if (register_object_types(dp_config)) {
		LOG(ERROR, "failed to register the object types");
		return -1;
	}

	// Mirror the dataplane's startup: a system agent seeds the initial
	// config generation, and only a ready segment accepts attaches.
	yanet_error *err = NULL;
	cp_config_lock(cp_config);

	struct agent *system_agent = dp_system_agent_new(
		cp_config, dp_config, "l3b_shm_test_system"
	);
	if (system_agent == NULL) {
		LOG(ERROR, "failed to create the system agent");
		cp_config_unlock(cp_config);
		return -1;
	}

	struct cp_config_gen *config_gen =
		cp_config_gen_new(system_agent, &err);
	if (config_gen == NULL) {
		log_error(&err, "failed to create the initial generation");
		cp_config_unlock(cp_config);
		return -1;
	}
	SET_OFFSET_OF(&cp_config->cp_config_gen, config_gen);
	cp_config_unlock(cp_config);

	dp_config_mark_ready(dp_config);

	struct yanet_shm shm = {
		.base = first_mapping,
		.size = SEGMENT_SIZE,
	};

	agent = agent_attach(&shm, 0, "l3b_shm_test", AGENT_MEMORY, &err);
	if (agent == NULL) {
		log_error(&err, "failed to attach the agent");
		return -1;
	}

	memcpy(real_server.destination_addr.v4.bytes, real4, NET4_LEN);
	memcpy(real_server.source_net.v4.addr, source4, NET4_LEN);
	memcpy(real_server.source_net.v4.mask, source_mask4, NET4_LEN);

	source_rule.net6s.items = &any6;
	source_rule.net6s.count = 1;
	source_rule.net4s.items = &any4;
	source_rule.net4s.count = 1;
	service_ports[0].from = SERVICE_PORT;
	service_ports[0].to = SERVICE_PORT;
	source_rule.port_ranges.items = service_ports;
	source_rule.port_ranges.count = 1;

	descriptor.source_filter_rules = &source_rule;
	descriptor.source_filter_rule_count = 1;
	descriptor.real_servers = &real_server;
	descriptor.real_server_count = 1;
	descriptor.ring_capacity = 64;

	struct cp_object *session_table =
		l3b_session_table_object_create(agent, "svc", 1, 4096, 0, &err);
	if (session_table == NULL) {
		log_error(&err, "failed to create the session table");
		return -1;
	}

	struct l3b_virtual_service_create_config config = {
		.agent = agent,
		.name = "svc",
		.session_table = session_table,
		.virtual_service = &descriptor,
	};

	service_object = l3b_virtual_service_create(&config, &err);
	if (service_object == NULL) {
		log_error(&err, "failed to create the virtual service");
		return -1;
	}

	const uint32_t ring_indexes[1] = {0};
	if (l3b_virtual_service_update_ring(
		    service_object, ring_indexes, 1, &err
	    )) {
		log_error(&err, "failed to fill the scheduler ring");
		return -1;
	}

	struct l3b_virtual_service_object *object = container_of(
		service_object, struct l3b_virtual_service_object, cp_object
	);
	published_service = &object->virtual_service;
	service_offset =
		(uintptr_t)published_service - (uintptr_t)first_mapping;
	object_offset = (uintptr_t)object - (uintptr_t)first_mapping;

	if (counter_registry_link(
		    &service_object->counter_registry, NULL, &err
	    )) {
		log_error(&err, "failed to link the counter registry");
		return -1;
	}
	service_counters = counter_storage_spawn(
		&agent->memory_context, NULL, &service_object->counter_registry
	);
	if (service_counters == NULL) {
		LOG(ERROR, "failed to spawn the counter storage");
		return -1;
	}
	counters_offset =
		(uintptr_t)service_counters - (uintptr_t)first_mapping;

	return 0;
}

// Map the segment a second time at an address of its own.
static int
setup_second_mapping(void) {
	for (uint8_t attempt = 0; attempt < 8; ++attempt) {
		void *reservation =
			mmap(NULL,
			     SEGMENT_SIZE,
			     PROT_NONE,
			     MAP_PRIVATE | MAP_ANONYMOUS,
			     -1,
			     0);
		if (reservation == MAP_FAILED) {
			return -1;
		}
		if (reservation == (void *)first_mapping) {
			munmap(reservation, SEGMENT_SIZE);
			continue;
		}

		second_mapping = (char *)mmap(
			reservation,
			SEGMENT_SIZE,
			PROT_READ | PROT_WRITE,
			MAP_SHARED | MAP_FIXED,
			segment_fd,
			0
		);
		if (second_mapping == MAP_FAILED) {
			second_mapping = NULL;
			munmap(reservation, SEGMENT_SIZE);
			return -1;
		}
		return 0;
	}
	return -1;
}

// The control-plane mapping must not be needed once the service is
// published; a dataplane that resolves a control-plane address faults here
// exactly like it would across processes.
static void
drop_first_mapping(void) {
	if (first_mapping == NULL) {
		return;
	}
	munmap(first_mapping, SEGMENT_SIZE);
	first_mapping = NULL;
}

static struct virtual_service *
second_service(void) {
	return (struct virtual_service *)(second_mapping + service_offset);
}

static struct cp_object *
second_object(void) {
	return (struct cp_object *)(second_mapping + object_offset);
}

static struct counter_storage *
second_counters(void) {
	return (struct counter_storage *)(second_mapping + counters_offset);
}

static int
build_client_tcp4(struct packet *packet) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) +
			   sizeof(struct rte_tcp_hdr);

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (packet->mbuf == NULL) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(packet->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = 0x45;
	ip->total_length = rte_cpu_to_be_16(pkt_len - sizeof(*eth));
	ip->time_to_live = 64;
	ip->next_proto_id = IPPROTO_TCP;
	memcpy(&ip->src_addr, client4, NET4_LEN);
	memcpy(&ip->dst_addr, vip4, NET4_LEN);
	ip->hdr_checksum = 0;
	ip->hdr_checksum = rte_ipv4_cksum(ip);

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip + 1);
	tcp->src_port = rte_cpu_to_be_16(CLIENT_PORT);
	tcp->dst_port = rte_cpu_to_be_16(SERVICE_PORT);
	tcp->rx_win = rte_cpu_to_be_16(1024);

	return parse_packet(packet);
}

// Before the first mapping goes away, both views of the published service
// must agree on the per-real counter ids, and the array must resolve inside
// the second mapping rather than at an address only the first mapping holds.
static int
test_counter_ids_resolve_in_both_mappings(void) {
	TEST_ASSERT_SUCCESS(setup_second_mapping(), "second mapping");

	struct virtual_service *service2 = second_service();

	const uint64_t *ids1 = ADDR_OF(&published_service->real_counter_ids);
	const uint64_t *ids2 = ADDR_OF(&service2->real_counter_ids);

	TEST_ASSERT(ids1 != NULL && ids2 != NULL, "per-real ids are published");
	TEST_ASSERT(
		ids2 >= (const uint64_t *)second_mapping &&
			(const char *)ids2 < second_mapping + SEGMENT_SIZE,
		"the id array resolves inside the reading mapping"
	);
	TEST_ASSERT_EQUAL(
		ids1[0], ids2[0], "both mappings name the same counter"
	);
	TEST_ASSERT(ids1[0] != COUNTER_INVALID, "real 0 carries a counter");

	uint64_t *slot = counter_get_address(ids2[0], second_counters());
	TEST_ASSERT(slot != NULL, "the counter resolves in the second mapping");
	TEST_ASSERT_EQUAL(slot[0], 0, "nothing is counted yet");

	return TEST_SUCCESS;
}

// With the control-plane mapping gone, the scheduler path counts the first
// packet on its real, and the second packet of the same flow counts through
// the pinned-session path even though the ring was emptied in between.
static int
test_pinned_path_counts_through_second_mapping(void) {
	drop_first_mapping();

	struct virtual_service *service2 = second_service();
	struct counter_storage *counters2 = second_counters();
	uint64_t *real_slot = counter_get_address(
		ADDR_OF(&service2->real_counter_ids)[0], counters2
	);

	struct packet first;
	TEST_ASSERT_SUCCESS(
		build_client_tcp4(&first), "build the first packet"
	);
	TEST_ASSERT_EQUAL(
		l3b_virtual_service_process(
			&worker, service2, &first, NULL, counters2
		),
		0,
		"the first packet is dispatched by the scheduler"
	);
	TEST_ASSERT_EQUAL(real_slot[0], 1, "the scheduler path counts real 0");
	free_packet(&first);

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		l3b_virtual_service_update_ring(second_object(), NULL, 0, &err),
		"empty the ring through the second mapping"
	);

	uint64_t before[2];
	memcpy(before, real_slot, sizeof(before));

	struct packet second;
	TEST_ASSERT_SUCCESS(
		build_client_tcp4(&second), "build the second packet"
	);
	TEST_ASSERT_EQUAL(
		l3b_virtual_service_process(
			&worker, service2, &second, NULL, counters2
		),
		0,
		"the second packet is dispatched through the pinned session"
	);
	TEST_ASSERT_EQUAL(
		real_slot[0],
		before[0] + 1,
		"the pinned path counts the flow's real without the ring"
	);
	TEST_ASSERT(
		real_slot[1] > before[1],
		"the pinned path counts the packet's bytes"
	);
	free_packet(&second);

	return TEST_SUCCESS;
}

int
main(void) {
	// The module-level destination queries stay in the shared header, but
	// the narrow service entry point tested here never consults them.
	(void)l3b_destination_filter_ip4;
	(void)l3b_destination_filter_ip6;

	log_enable_name("info");

	LOG(INFO, "=== Starting l3b shm test suite ===");

	if (setup_dpdk()) {
		LOG(ERROR, "failed to set up dpdk");
		return TEST_FAILED;
	}

	if (setup_segment()) {
		LOG(ERROR, "failed to back the segment with a file");
		return TEST_FAILED;
	}

	if (publish_service()) {
		LOG(ERROR, "failed to publish the virtual service");
		return TEST_FAILED;
	}

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"counter_ids_resolve_in_both_mappings",
		 test_counter_ids_resolve_in_both_mappings},
		{"pinned_path_counts_through_second_mapping",
		 test_pinned_path_counts_through_second_mapping},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; idx++) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	drop_first_mapping();
	if (second_mapping != NULL) {
		munmap(second_mapping, SEGMENT_SIZE);
		second_mapping = NULL;
	}
	close(segment_fd);

	if (failed == 0) {
		LOG(INFO, "=== All %zu l3b shm tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu l3b shm tests failed ===", failed, total
		);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
