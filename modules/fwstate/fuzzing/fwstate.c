#include <netinet/in.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_cycles.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>

#include "common/container_of.h"
#include "controlplane/agent/agent.h"
#include "counters/counters.h"
#include "lib/dataplane/time/clock.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwtable.h"
#include "lib/fwstate/types.h"
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/dataplane/dataplane.h"
#include "modules/fwstate/objects/fwstate_map_object.h"

#include "lib/fuzzing/fuzzing.h"

// Forward declaration of DPDK internal function
extern void
set_tsc_freq(void);

static struct fuzzing_params fuzz_params = {0};

// Minimal agent used to drive fwstate_map_object_config_new.
//
// The fuzz target never publishes modules to a real agent, but the
// fwstate-map API allocates through agent->memory_context. We use a
// static agent whose memory_context shares fuzz_params' block
// allocator, mirroring how production wires a named fwstate-map owner.
static struct agent fuzz_agent;

// Initialize a fwmap_config_t for the fuzz target, selecting v4 or v6
// key sizes and callbacks based on the kind.
static void
fwstate_init_config_for_fuzz(
	fwmap_config_t *config,
	enum fwtable_kind kind,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	if (index_size == 0) {
		index_size = 1024 * 1024;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = 1024;
	}

	if (kind == FWTABLE_KIND_V6) {
		config->key_size = sizeof(struct fw6_state_key);
		config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW6;
		config->copy_key_fn_id = FWMAP_COPY_KEY_FW6;
	} else {
		config->key_size = sizeof(struct fw4_state_key);
		config->key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
		config->copy_key_fn_id = FWMAP_COPY_KEY_FW4;
	}

	config->value_size = sizeof(struct fw_state_value);
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE;
	config->hash_seed = 0;
	config->hash_fn_id = FWMAP_HASH_FNV1A;
	config->worker_count = worker_count;
	config->index_size = index_size;
	config->extra_bucket_count = extra_bucket_count;
	config->rand_fn_id = FWMAP_RAND_DEFAULT;
}

static int
fwstate_test_config(struct cp_module **cp_module) {
	struct fwstate_module_config *config =
		(struct fwstate_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct fwstate_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"fwstate_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"fwstate_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;

	// The fwstate handler unconditionally resolves per-worker counter
	// addresses via counter_get_address(), so a valid counter_registry and
	// counter_storage must be provided or the handler dereferences NULL.
	// size=2 counters hold [packets, bytes]; size=1 counters hold
	// [packets].
	if (counter_registry_init(
		    &config->cp_module.counter_registry,
		    &config->cp_module.memory_context,
		    0
	    )) {
		return -ENOMEM;
	}

	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"fwstate_sync", 2, &config->sync_packets_counter_id},
		{"fwstate_passthrough", 2, &config->passthrough_counter_id},
		{"fwstate_sync_v4_inserted",
		 1,
		 &config->sync_v4_inserted_counter_id},
		{"fwstate_sync_v6_inserted",
		 1,
		 &config->sync_v6_inserted_counter_id},
		{"fwstate_sync_v4_insert_failed",
		 1,
		 &config->sync_v4_insert_failed_counter_id},
		{"fwstate_sync_v6_insert_failed",
		 1,
		 &config->sync_v6_insert_failed_counter_id},
		{"fwstate_external_dropped",
		 2,
		 &config->external_dropped_counter_id},
		{"fwstate_internal_forwarded",
		 2,
		 &config->internal_forwarded_counter_id},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			NULL
		);
		if (id == (uint64_t)-1) {
			return -ENOMEM;
		}
		*counters[i].dst = id;
	}

	if (counter_registry_link(
		    &config->cp_module.counter_registry, NULL, NULL
	    )) {
		return -ENOMEM;
	}

	struct counter_storage *cs = counter_storage_spawn(
		&fuzz_params.mctx, NULL, &config->cp_module.counter_registry
	);
	if (cs == NULL) {
		return -ENOMEM;
	}
	SET_OFFSET_OF(&fuzz_params.module_ectx.counter_storage, cs);

	// Allocate persistent fwstate-map objects, one v4 and one v6.
	// The fuzz target bypasses cp_object_init (which needs a real
	// dp_config to resolve the object type) and directly allocates the
	// fwtable — the fuzzer only exercises the dataplane hot path.
	memset(&fuzz_agent, 0, sizeof(fuzz_agent));
	memory_context_init_from(
		&fuzz_agent.memory_context, &fuzz_params.mctx, "fuzz-agent"
	);

	static struct fwstate_map_object v4_map_obj;
	static struct fwstate_map_object v6_map_obj;
	struct fwstate_map_object *v4_map = &v4_map_obj;
	struct fwstate_map_object *v6_map = &v6_map_obj;
	memset(v4_map, 0, sizeof(*v4_map));
	memset(v6_map, 0, sizeof(*v6_map));

	fwmap_config_t fw4_cfg;
	fwstate_init_config_for_fuzz(&fw4_cfg, FWTABLE_KIND_V4, 1024, 64, 1);
	if (fwtable_insert_layer_cp(
		    &v4_map->table, &fw4_cfg, &fuzz_agent.memory_context
	    )) {
		return -ENOMEM;
	}

	fwmap_config_t fw6_cfg;
	fwstate_init_config_for_fuzz(&fw6_cfg, FWTABLE_KIND_V6, 1024, 64, 1);
	if (fwtable_insert_layer_cp(
		    &v6_map->table, &fw6_cfg, &fuzz_agent.memory_context
	    )) {
		return -ENOMEM;
	}

	// Wire the object links into the module ectx so the handler can
	// resolve the fwtables via object_link_get_address.
	config->v4_object_link_idx = 0;
	config->v6_object_link_idx = 1;

	static struct object_ectx v4_oectx;
	static struct object_ectx v6_oectx;
	static struct module_object_link_ectx links[2];

	SET_OFFSET_OF(&v4_oectx.cp_object, &v4_map->cp_object);
	SET_OFFSET_OF(&v6_oectx.cp_object, &v6_map->cp_object);
	SET_OFFSET_OF(&links[0].object_ectx, &v4_oectx);
	SET_OFFSET_OF(&links[1].object_ectx, &v6_oectx);
	fuzz_params.module_ectx.object_link_count = 2;
	SET_OFFSET_OF(&fuzz_params.module_ectx.object_links, &links[0]);

	// Configure sync settings
	uint8_t multicast_addr[16] = {
		0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01
	};
	memcpy(config->sync_config.dst_addr_multicast, multicast_addr, 16);
	config->sync_config.port_multicast = rte_cpu_to_be_16(9999);

	// Set timeouts
	config->sync_config.timeouts.tcp_syn_ack = 120000000000ULL;
	config->sync_config.timeouts.tcp_syn = 120000000000ULL;
	config->sync_config.timeouts.tcp_fin = 120000000000ULL;
	config->sync_config.timeouts.tcp = 120000000000ULL;
	config->sync_config.timeouts.udp = 30000000000ULL;
	config->sync_config.timeouts.default_ = 16000000000ULL;

	*cp_module = (struct cp_module *)config;
	return 0;
}

static int
fuzz_setup() {
	if (fuzzing_params_init(
		    &fuzz_params, "fwstate fuzzing", new_module_fwstate
	    ) != 0) {
		return EXIT_FAILURE;
	}

	// Create a minimal dp_worker structure for fwstate
	fuzz_params.worker =
		memory_balloc(&fuzz_params.mctx, sizeof(struct dp_worker));
	if (fuzz_params.worker == NULL) {
		return -ENOMEM;
	}
	memset(fuzz_params.worker, 0, sizeof(struct dp_worker));
	fuzz_params.worker->idx = 0;

	// Initialize TSC frequency (DPDK internal, needed for rte_get_tsc_hz())
	set_tsc_freq();

	// Initialize TSC clock for fwstate (needed for timeouts)
	if (tsc_clock_init(&fuzz_params.worker->clock) != 0) {
		return -ENOMEM;
	}

	return fwstate_test_config(&fuzz_params.cp_module);
}

// Helper to build a valid sync packet wrapper around fuzzer input
static void
build_sync_packet(
	uint8_t *pkt_data, const uint8_t *sync_payload, size_t payload_len
) {
	// Ethernet header with multicast destination
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)pkt_data;
	eth->dst_addr.addr_bytes[0] = 0x01; // Multicast
	eth->dst_addr.addr_bytes[1] = 0x00;
	eth->dst_addr.addr_bytes[2] = 0x5e;
	eth->dst_addr.addr_bytes[3] = 0x00;
	eth->dst_addr.addr_bytes[4] = 0x00;
	eth->dst_addr.addr_bytes[5] = 0x01;
	memset(eth->src_addr.addr_bytes, 0, 6);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN);

	// VLAN header
	struct rte_vlan_hdr *vlan = (struct rte_vlan_hdr *)(eth + 1);
	vlan->vlan_tci = 0;
	vlan->eth_proto = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	// IPv6 header
	struct rte_ipv6_hdr *ipv6 = (struct rte_ipv6_hdr *)(vlan + 1);
	ipv6->vtc_flow =
		rte_cpu_to_be_32(0x60000000); // IPv6, no traffic class/flow
	ipv6->payload_len =
		rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len);
	ipv6->proto = IPPROTO_UDP;
	ipv6->hop_limits = 64;

	// Source: all zeros for internal, or use fuzzer data for external
	memset(ipv6->src_addr, 0, 16);

	// Destination: ff02::1 (multicast)
	memset(ipv6->dst_addr, 0, 16);
	ipv6->dst_addr[0] = 0xff;
	ipv6->dst_addr[1] = 0x02;
	ipv6->dst_addr[15] = 0x01;

	// UDP header
	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ipv6 + 1);
	udp->src_port = rte_cpu_to_be_16(12345);
	udp->dst_port = rte_cpu_to_be_16(9999); // Configured multicast port
	udp->dgram_len =
		rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len);
	udp->dgram_cksum = 0;

	// Copy sync frame payload
	memcpy(udp + 1, sync_payload, payload_len);

	// Calculate UDP checksum
	udp->dgram_cksum = rte_ipv6_udptcp_cksum(ipv6, udp);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	if (size > (MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM)) {
		return 0;
	}

	// If input size is a multiple of sync frame size (56 bytes),
	// wrap it as a valid sync packet to test sync processing paths
	if (size > 0 && size % sizeof(struct fw_state_sync_frame) == 0 &&
	    size <= 512) { // Reasonable limit for sync frames
		const size_t hdr_size = sizeof(struct rte_ether_hdr) +
					sizeof(struct rte_vlan_hdr) +
					sizeof(struct rte_ipv6_hdr) +
					sizeof(struct rte_udp_hdr);

		uint8_t packet_buffer[MBUF_MAX_SIZE];
		build_sync_packet(packet_buffer, data, size);

		return fuzzing_process_packet(
			&fuzz_params, packet_buffer, hdr_size + size
		);
	} else {
		// Use raw fuzzer input for other packet types
		return fuzzing_process_packet(&fuzz_params, data, size);
	}
}
