/*
 * Bounded packet-path fuzz target for the l3b balancer module.
 *
 * A persistent shared-memory harness publishes one l3b configuration
 * linked to a virtual service with IPv4/IPv6 destination and source
 * filters, one worker's session table, no real servers and an empty
 * scheduler ring, and every input runs through the plain device's
 * output entry as one published-pipeline round: classification,
 * fragment guards, source admission and the empty-ring rejection all
 * execute, while session pinning and encapsulation stay out of scope
 * because an empty ring never selects a real. Inputs shorter than the
 * smallest Ethernet frame select a built-in seed frame whose service
 * counter deltas and output-versus-drop membership the target asserts;
 * every other input is a raw frame. Production attaches l3b at device
 * input while this harness attaches it at device output — a topology
 * note only, the observable is output-versus-drop membership either
 * way.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <netinet/ip_icmp.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "api/agent.h"
#include "api/config.h"

#include "common/filter_views.h"
#include "common/network.h"

#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/stages.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include "modules/l3b/api/controlplane.h"
#include "objects/l3b/api/l3b_session_table_object.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

#define L3B_FUZZ_MAX_FRAME 1500
// Smallest built-in seed frame: Ethernet plus a bare IPv4 header.
#define L3B_FUZZ_MIN_RAW 34
#define L3B_FUZZ_TCP4_LEN                                                      \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) +          \
	 sizeof(struct rte_tcp_hdr))
#define L3B_FUZZ_TCP6_LEN                                                      \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) +          \
	 sizeof(struct rte_tcp_hdr))
#define L3B_FUZZ_ICMP4_LEN                                                     \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) + 12)
#define L3B_FUZZ_SHORT4_LEN                                                    \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr))

#define L3B_FUZZ_SERVICE_PORT 80
#define L3B_FUZZ_CLIENT_PORT 12345

#define L3B_FUZZ_CP_MEMORY (64u << 20)
#define L3B_FUZZ_DP_MEMORY (4u << 20)
#define L3B_FUZZ_AGENT_MEMORY (16u << 20)
// One second past harness boot, held fixed so packet deadlines never move.
#define L3B_FUZZ_TIME_NS 1000000000ull

#define L3B_FUZZ_DEVICE "port0"
#define L3B_FUZZ_CONFIG "fuzz"
#define L3B_FUZZ_SERVICE "svc"

static const uint8_t client4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t stranger4[NET4_LEN] = {172, 16, 0, 1};
static const uint8_t vip4[NET4_LEN] = {192, 168, 1, 1};
static const uint8_t elsewhere4[NET4_LEN] = {198, 51, 100, 7};

static const uint8_t client6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t stranger6[NET6_LEN] = {
	0xfc, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t vip6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};

// Descriptors must outlive the calls that copy them into shared memory.
static struct net4 source_net4;
static struct net6 source_net6;
static struct filter_port_range source_ports[1];
static struct l3b_source_filter_rule source_rule;

static struct net4 destination_net4;
static struct net6 destination_net6;
static struct filter_proto_range service_protos[3];
static struct l3b_destination_filter_rule destination_rules[2];

static struct l3b_virtual_service service_descriptor;

struct l3b_fuzz_state {
	struct dataplane_ut *ut;
	struct agent *agent;
	struct cp_object *service;
	uintptr_t published_ectx;
};

static struct l3b_fuzz_state fuzz_state;

struct l3b_fuzz_seed {
	size_t (*build)(uint8_t *frame);
	// Whether trailing input bytes may extend the seed frame; a seed
	// whose transport region starts empty must refuse, because extra
	// bytes would change its classification outcome.
	bool payload;
	// Expected per-input packet deltas of the service counters and the
	// expected round membership of the packet.
	uint64_t incoming;
	uint64_t filter_rejected;
	uint64_t ring_empty;
	size_t dropped;
	size_t forwarded;
};

static void
l3b_fuzz_abort(const char *what) {
	LOG(ERROR, "l3b fuzz oracle violated: %s", what);
	abort();
}

static size_t
build_tcp4(
	uint8_t *frame,
	const uint8_t src[NET4_LEN],
	const uint8_t dst[NET4_LEN],
	bool fragment
) {
	memset(frame, 0, L3B_FUZZ_TCP4_LEN);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)frame;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = RTE_IPV4_VHL_DEF;
	ip->total_length = rte_cpu_to_be_16(L3B_FUZZ_TCP4_LEN - sizeof(*eth));
	ip->next_proto_id = IPPROTO_TCP;
	if (fragment) {
		ip->fragment_offset = rte_cpu_to_be_16(RTE_IPV4_HDR_MF_FLAG);
	}
	memcpy(&ip->src_addr, src, NET4_LEN);
	memcpy(&ip->dst_addr, dst, NET4_LEN);
	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip + 1);
	tcp->src_port = rte_cpu_to_be_16(L3B_FUZZ_CLIENT_PORT);
	tcp->dst_port = rte_cpu_to_be_16(L3B_FUZZ_SERVICE_PORT);
	tcp->rx_win = rte_cpu_to_be_16(1024);
	return L3B_FUZZ_TCP4_LEN;
}

static size_t
build_tcp6(
	uint8_t *frame, const uint8_t src[NET6_LEN], const uint8_t dst[NET6_LEN]
) {
	memset(frame, 0, L3B_FUZZ_TCP6_LEN);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)frame;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	struct rte_ipv6_hdr *ip = (struct rte_ipv6_hdr *)(eth + 1);
	ip->payload_len = rte_cpu_to_be_16(
		L3B_FUZZ_TCP6_LEN - sizeof(*eth) - sizeof(*ip)
	);
	ip->proto = IPPROTO_TCP;
	memcpy(&ip->src_addr, src, NET6_LEN);
	memcpy(&ip->dst_addr, dst, NET6_LEN);
	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip + 1);
	tcp->src_port = rte_cpu_to_be_16(L3B_FUZZ_CLIENT_PORT);
	tcp->dst_port = rte_cpu_to_be_16(L3B_FUZZ_SERVICE_PORT);
	tcp->rx_win = rte_cpu_to_be_16(1024);
	return L3B_FUZZ_TCP6_LEN;
}

static size_t
build_udp4(uint8_t *frame) {
	const size_t len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) +
			   sizeof(struct rte_udp_hdr);
	memset(frame, 0, len);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)frame;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = RTE_IPV4_VHL_DEF;
	ip->total_length = rte_cpu_to_be_16(len - sizeof(*eth));
	ip->next_proto_id = IPPROTO_UDP;
	memcpy(&ip->src_addr, client4, NET4_LEN);
	memcpy(&ip->dst_addr, vip4, NET4_LEN);
	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ip + 1);
	udp->src_port = rte_cpu_to_be_16(L3B_FUZZ_CLIENT_PORT);
	udp->dst_port = rte_cpu_to_be_16(L3B_FUZZ_SERVICE_PORT);
	udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
	return len;
}

// An Echo request towards dst: a complete eight-byte header plus payload,
// the shape the balancer answers in place once reals exist.
static size_t
build_icmp4_echo(uint8_t *frame, const uint8_t dst[NET4_LEN]) {
	memset(frame, 0, L3B_FUZZ_ICMP4_LEN);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)frame;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = RTE_IPV4_VHL_DEF;
	ip->total_length = rte_cpu_to_be_16(L3B_FUZZ_ICMP4_LEN - sizeof(*eth));
	ip->next_proto_id = IPPROTO_ICMP;
	memcpy(&ip->src_addr, client4, NET4_LEN);
	memcpy(&ip->dst_addr, dst, NET4_LEN);
	frame[sizeof(*eth) + sizeof(*ip)] = ICMP_ECHO;
	return L3B_FUZZ_ICMP4_LEN;
}

// An ICMP packet whose transport region holds no bytes at all: not
// eligible for balancing, so it must pass through untouched.
static size_t
build_short_icmp4(uint8_t *frame, const uint8_t dst[NET4_LEN]) {
	memset(frame, 0, L3B_FUZZ_SHORT4_LEN);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)frame;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = RTE_IPV4_VHL_DEF;
	ip->total_length = rte_cpu_to_be_16(sizeof(*ip));
	ip->next_proto_id = IPPROTO_ICMP;
	memcpy(&ip->src_addr, client4, NET4_LEN);
	memcpy(&ip->dst_addr, dst, NET4_LEN);
	return L3B_FUZZ_SHORT4_LEN;
}

static size_t
build_admitted4(uint8_t *frame) {
	return build_tcp4(frame, client4, vip4, false);
}

static size_t
build_source_rejected4(uint8_t *frame) {
	return build_tcp4(frame, stranger4, vip4, false);
}

static size_t
build_destination_missed4(uint8_t *frame) {
	return build_tcp4(frame, client4, elsewhere4, false);
}

static size_t
build_admitted6(uint8_t *frame) {
	return build_tcp6(frame, client6, vip6);
}

static size_t
build_source_rejected6(uint8_t *frame) {
	return build_tcp6(frame, stranger6, vip6);
}

static size_t
build_echo_matched4(uint8_t *frame) {
	return build_icmp4_echo(frame, vip4);
}

static size_t
build_echo_unmatched4(uint8_t *frame) {
	return build_icmp4_echo(frame, elsewhere4);
}

static size_t
build_fragment4(uint8_t *frame) {
	return build_tcp4(frame, client4, vip4, true);
}

static size_t
build_short_icmp_matched4(uint8_t *frame) {
	return build_short_icmp4(frame, vip4);
}

static const struct l3b_fuzz_seed l3b_fuzz_seeds[] = {
	{.build = build_admitted4,
	 .payload = true,
	 .incoming = 1,
	 .ring_empty = 1,
	 .dropped = 1},
	{.build = build_source_rejected4,
	 .payload = true,
	 .incoming = 1,
	 .filter_rejected = 1,
	 .dropped = 1},
	{.build = build_destination_missed4, .payload = true, .dropped = 1},
	{.build = build_admitted6,
	 .payload = true,
	 .incoming = 1,
	 .ring_empty = 1,
	 .dropped = 1},
	{.build = build_source_rejected6,
	 .payload = true,
	 .incoming = 1,
	 .filter_rejected = 1,
	 .dropped = 1},
	// A matched Echo request is refused while the service has no reals;
	// the in-place reply rewrite stays uncovered until that scope exists.
	{.build = build_echo_matched4,
	 .payload = true,
	 .incoming = 1,
	 .dropped = 1},
	{.build = build_echo_unmatched4, .payload = true, .forwarded = 1},
	{.build = build_fragment4, .payload = true, .dropped = 1},
	{.build = build_short_icmp_matched4, .forwarded = 1},
	{.build = build_udp4,
	 .payload = true,
	 .incoming = 1,
	 .ring_empty = 1,
	 .dropped = 1},
};

#define L3B_FUZZ_SEED_COUNT (sizeof(l3b_fuzz_seeds) / sizeof(l3b_fuzz_seeds[0]))

struct l3b_fuzz_snapshot {
	uint64_t incoming;
	uint64_t filter_rejected;
	uint64_t ring_empty;
};

static uint64_t
l3b_fuzz_counter(const char *counter) {
	uint64_t packets_and_bytes[2];
	if (l3b_virtual_service_counter_read(
		    fuzz_state.published_ectx,
		    L3B_FUZZ_SERVICE,
		    counter,
		    packets_and_bytes
	    ) != 0) {
		l3b_fuzz_abort("service counter unreadable");
	}
	return packets_and_bytes[0];
}

static void
l3b_fuzz_snapshot(struct l3b_fuzz_snapshot *snapshot) {
	snapshot->incoming = l3b_fuzz_counter(L3B_COUNTER_INCOMING);
	snapshot->filter_rejected =
		l3b_fuzz_counter(L3B_COUNTER_FILTER_REJECTED);
	snapshot->ring_empty = l3b_fuzz_counter(L3B_COUNTER_RING_EMPTY);
}

// Drive one frame through the published output entry and, for a seed,
// assert the service counter deltas and the packet's round membership.
static void
l3b_fuzz_run(
	const uint8_t *frame, size_t size, const struct l3b_fuzz_seed *seed
) {
	if (dataplane_ut_begin_round(fuzz_state.ut, 0) != 0) {
		l3b_fuzz_abort("round start refused");
	}

	struct packet_list input;
	packet_list_init(&input);
	if (dataplane_ut_stage_input_append(&input, frame, size) != 0) {
		// A built-in seed frame must parse; its rejection is a
		// fixture bug, not an input to ignore. A raw frame is the
		// shared parser's to reject, and it released the packet. The
		// opened round holds neither packets nor the round lock, and
		// the next round start replaces it.
		if (seed != NULL) {
			l3b_fuzz_abort("seed frame rejected by the parser");
		}
		return;
	}

	struct l3b_fuzz_snapshot before = {0};
	if (seed != NULL) {
		l3b_fuzz_snapshot(&before);
	}

	struct dataplane_ut_stage stage = {
		.worker_idx = 0,
		.device_id = 0,
		.output = true,
	};
	struct dataplane_ut_round_result result;
	if (dataplane_ut_run_stage(fuzz_state.ut, &stage, &input, &result) !=
	    0) {
		l3b_fuzz_abort("published stage execution refused");
	}

	size_t dropped = packet_list_count(&result.drop);
	size_t forwarded = packet_list_count(&result.output);
	if (seed != NULL) {
		struct l3b_fuzz_snapshot after;
		l3b_fuzz_snapshot(&after);
		if (after.incoming - before.incoming != seed->incoming ||
		    after.filter_rejected - before.filter_rejected !=
			    seed->filter_rejected ||
		    after.ring_empty - before.ring_empty != seed->ring_empty ||
		    dropped != seed->dropped || forwarded != seed->forwarded) {
			LOG(ERROR,
			    "seed outcome mismatch: incoming +%llu want %llu,"
			    " filter_rejected +%llu want %llu, ring_empty"
			    " +%llu want %llu, dropped %zu want %zu,"
			    " forwarded %zu want %zu",
			    (unsigned long long)(after.incoming -
						 before.incoming),
			    (unsigned long long)seed->incoming,
			    (unsigned long long)(after.filter_rejected -
						 before.filter_rejected),
			    (unsigned long long)seed->filter_rejected,
			    (unsigned long long)(after.ring_empty -
						 before.ring_empty),
			    (unsigned long long)seed->ring_empty,
			    dropped,
			    seed->dropped,
			    forwarded,
			    seed->forwarded);
			l3b_fuzz_abort("seed outcome mismatch");
		}
	} else if (dropped + forwarded != 1) {
		l3b_fuzz_abort("parsed frame left the round unaccounted");
	}

	dataplane_ut_round_result_free(&result);
	if (dataplane_ut_mempool_outstanding(fuzz_state.ut) != 0) {
		l3b_fuzz_abort("a packet allocation survived the round");
	}
}

static int
l3b_fuzz_fail(yanet_error **err, const char *what) {
	char *message = yanet_error_format(*err);
	LOG(ERROR,
	    "l3b fuzz fixture: %s: %s",
	    what,
	    message != NULL ? message : "unknown");
	free(message);
	return -1;
}

static void
l3b_fuzz_descriptors(void) {
	// Clients of the admitted 10.0.0.0/8 and 2001:db8::/32 networks.
	source_net4 =
		(struct net4){.addr = {10, 0, 0, 0}, .mask = {255, 0, 0, 0}};
	source_net6 = (struct net6){.addr = {0x20, 0x01, 0x0d, 0xb8},
				    .mask = {0xff, 0xff, 0xff, 0xff}};
	source_ports[0] = (struct filter_port_range){
		.from = L3B_FUZZ_SERVICE_PORT,
		.to = L3B_FUZZ_SERVICE_PORT,
	};
	memset(&source_rule, 0, sizeof(source_rule));
	source_rule.net6s =
		(struct filter_net6s){.items = &source_net6, .count = 1};
	source_rule.net4s =
		(struct filter_net4s){.items = &source_net4, .count = 1};
	source_rule.port_ranges =
		(struct filter_port_ranges){.items = source_ports, .count = 1};

	// The service addresses, one rule per family; the IPv6 rule covers
	// the 2001:db8:ffff::/64 the service address lives in.
	destination_net4 = (struct net4){.addr = {192, 168, 1, 1},
					 .mask = {255, 255, 255, 255}};
	destination_net6 = (struct net6){
		.addr = {0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff},
		.mask = {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	};
	// Transport keys carry the protocol in the high byte and its subtype
	// in the low byte: the TCP flags byte or the ICMP message type.
	service_protos[0] =
		(struct filter_proto_range){.from = 0x0108, .to = 0x0108};
	service_protos[1] =
		(struct filter_proto_range){.from = 0x0600, .to = 0x06ff};
	service_protos[2] =
		(struct filter_proto_range){.from = 0x1100, .to = 0x1100};
	memset(destination_rules, 0, sizeof(destination_rules));
	destination_rules[0].net4s =
		(struct filter_net4s){.items = &destination_net4, .count = 1};
	destination_rules[0].proto_ranges = (struct filter_proto_ranges
	){.items = service_protos, .count = 3};
	destination_rules[0].virtual_service = L3B_FUZZ_SERVICE;
	destination_rules[1].net6s =
		(struct filter_net6s){.items = &destination_net6, .count = 1};
	destination_rules[1].proto_ranges = (struct filter_proto_ranges
	){.items = service_protos, .count = 3};
	destination_rules[1].virtual_service = L3B_FUZZ_SERVICE;

	// No reals and no ring slots: admitted transport can only be counted
	// and refused, which keeps session pinning out of this target.
	memset(&service_descriptor, 0, sizeof(service_descriptor));
	service_descriptor.source_filter_rules = &source_rule;
	service_descriptor.source_filter_rule_count = 1;
	service_descriptor.ring_capacity = 0;
}

static int
l3b_fuzz_setup(void) {
	const char *devices[] = {L3B_FUZZ_DEVICE};
	const char *modules[] = {"l3b"};
	const char *devices_to_load[] = {"plain"};
	const char *objects_to_load[] = {
		"l3b_virtual_service",
		"l3b_session_table",
	};
	struct dataplane_ut_config config = {
		.cp_memory = L3B_FUZZ_CP_MEMORY,
		.dp_memory = L3B_FUZZ_DP_MEMORY,
		.worker_count = 1,
		.devices = devices,
		.device_count = 1,
		.modules = modules,
		.module_count = 1,
		.devices_to_load = devices_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objects_to_load,
		.objects_to_load_count = 2,
	};
	fuzz_state.ut = dataplane_ut_new(&config);
	if (fuzz_state.ut == NULL) {
		LOG(ERROR, "l3b fuzz fixture: harness construction failed");
		return -1;
	}
	dataplane_ut_set_time_ns(fuzz_state.ut, L3B_FUZZ_TIME_NS);

	yanet_error *err = NULL;
	fuzz_state.agent = agent_attach(
		dataplane_ut_shm(fuzz_state.ut),
		0,
		"l3b-fuzz",
		L3B_FUZZ_AGENT_MEMORY,
		&err
	);
	if (fuzz_state.agent == NULL) {
		return l3b_fuzz_fail(&err, "agent attach failed");
	}

	l3b_fuzz_descriptors();

	struct cp_object *session_table = l3b_session_table_object_create(
		fuzz_state.agent, L3B_FUZZ_SERVICE, 1, 4096, 0, &err
	);
	if (session_table == NULL) {
		return l3b_fuzz_fail(&err, "session table creation failed");
	}

	struct l3b_virtual_service_create_config create = {
		.agent = fuzz_state.agent,
		.name = L3B_FUZZ_SERVICE,
		.session_table = session_table,
		.virtual_service = &service_descriptor,
	};
	fuzz_state.service = l3b_virtual_service_create(&create, &err);
	if (fuzz_state.service == NULL) {
		return l3b_fuzz_fail(&err, "virtual service creation failed");
	}
	struct cp_object *objects[] = {fuzz_state.service};
	if (agent_update_objects(fuzz_state.agent, 1, objects, &err) != 0) {
		return l3b_fuzz_fail(&err, "object publication failed");
	}

	struct cp_module *module =
		l3b_module_config_new(fuzz_state.agent, L3B_FUZZ_CONFIG, &err);
	if (module == NULL) {
		return l3b_fuzz_fail(&err, "module creation failed");
	}
	if (l3b_module_config_update(module, destination_rules, 2, &err) != 0) {
		return l3b_fuzz_fail(&err, "module configuration failed");
	}
	struct cp_module *modules_published[] = {module};
	if (agent_update_modules(
		    fuzz_state.agent, 1, modules_published, &err
	    ) != 0) {
		return l3b_fuzz_fail(&err, "module publication failed");
	}

	const char *chain_types[] = {"l3b"};
	const char *chain_names[] = {L3B_FUZZ_CONFIG};
	struct cp_chain_config *chain = cp_chain_config_create(
		L3B_FUZZ_CONFIG "_chain", 1, chain_types, chain_names
	);
	if (chain == NULL) {
		return l3b_fuzz_fail(&err, "chain allocation failed");
	}
	struct cp_function_config *function =
		cp_function_config_create(L3B_FUZZ_CONFIG, 1);
	if (function == NULL) {
		cp_chain_config_free(chain);
		return l3b_fuzz_fail(&err, "function allocation failed");
	}
	if (cp_function_config_set_chain(function, 0, chain, 1) != 0) {
		return l3b_fuzz_fail(&err, "chain assignment failed");
	}
	struct cp_function_config *functions[] = {function};
	if (agent_update_functions(fuzz_state.agent, 1, functions, &err) != 0) {
		return l3b_fuzz_fail(&err, "function publication failed");
	}
	cp_function_config_free(function);

	struct cp_pipeline_config *pipeline =
		cp_pipeline_config_create(L3B_FUZZ_CONFIG, 1);
	if (pipeline == NULL) {
		return l3b_fuzz_fail(&err, "pipeline allocation failed");
	}
	if (cp_pipeline_config_set_function(pipeline, 0, L3B_FUZZ_CONFIG) !=
	    0) {
		return l3b_fuzz_fail(&err, "pipeline assembly failed");
	}
	struct cp_pipeline_config *pipelines[] = {pipeline};
	if (agent_update_pipelines(fuzz_state.agent, 1, pipelines, &err) != 0) {
		return l3b_fuzz_fail(&err, "pipeline publication failed");
	}
	cp_pipeline_config_free(pipeline);

	// The device exposes the module on its output entry: packets the
	// balancer passes through reach the egress buckets, dropped ones
	// never do, and no second routing module is needed to tell them
	// apart.
	struct cp_device_plain_config *device_config =
		cp_device_plain_config_new(L3B_FUZZ_DEVICE, 0, 1, &err);
	if (device_config == NULL) {
		return l3b_fuzz_fail(&err, "device allocation failed");
	}
	if (cp_device_plain_config_set_output_pipeline(
		    device_config, 0, L3B_FUZZ_CONFIG, 1
	    ) != 0) {
		return l3b_fuzz_fail(&err, "device pipeline assignment failed");
	}
	struct cp_device *device =
		cp_device_plain_new(fuzz_state.agent, device_config, &err);
	cp_device_plain_config_free(device_config);
	if (device == NULL) {
		return l3b_fuzz_fail(&err, "device creation failed");
	}
	struct cp_device *devices_published[] = {device};
	if (agent_update_devices(
		    fuzz_state.agent, 1, devices_published, &err
	    ) != 0) {
		return l3b_fuzz_fail(&err, "device publication failed");
	}

	fuzz_state.published_ectx =
		dataplane_ut_published_ectx(fuzz_state.ut, 0);
	if (fuzz_state.published_ectx == 0) {
		l3b_fuzz_abort("no published execution context");
	}

	// One pass over every seed proves the fixture reaches the module
	// before the first fuzzer input: each dispatch must be counted and
	// land on its expected side of the round.
	uint8_t frame[L3B_FUZZ_MAX_FRAME];
	for (size_t idx = 0; idx < L3B_FUZZ_SEED_COUNT; ++idx) {
		const struct l3b_fuzz_seed *seed = &l3b_fuzz_seeds[idx];
		size_t size = seed->build(frame);
		l3b_fuzz_run(frame, size, seed);
	}
	return 0;
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_state.ut == NULL) {
		if (l3b_fuzz_setup() != 0) {
			exit(1);
		}
	}

	if (size == 0 || size > L3B_FUZZ_MAX_FRAME) {
		return 0;
	}

	uint8_t frame[L3B_FUZZ_MAX_FRAME];
	const struct l3b_fuzz_seed *seed = NULL;
	size_t frame_size;
	if (size >= L3B_FUZZ_MIN_RAW) {
		memcpy(frame, data, size);
		frame_size = size;
	} else {
		seed = &l3b_fuzz_seeds[data[0] % L3B_FUZZ_SEED_COUNT];
		frame_size = seed->build(frame);
		if (frame_size >= L3B_FUZZ_MAX_FRAME) {
			// No builder may exceed the frame budget; the room
			// arithmetic below relies on it.
			l3b_fuzz_abort("seed frame exceeds the frame budget");
		}
		if (seed->payload && size > 1) {
			size_t room = L3B_FUZZ_MAX_FRAME - frame_size;
			size_t extra = size - 1;
			if (extra > room) {
				extra = room;
			}
			memcpy(frame + frame_size, data + 1, extra);
			frame_size += extra;
		}
	}

	l3b_fuzz_run(frame, frame_size, seed);
	return 0;
}
