#include <netinet/in.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE
#include <rte_build_config.h>	// RTE_PKTMBUF_HEADROOM
#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/types.h"
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/dataplane/dataplane.h"

#include "lib/utils/packet.h"

#define ARENA_SIZE (64 << 20)
#define MAX_SYNC_PACKETS 16

struct sync_packet_slot {
	struct rte_mbuf *mbuf;
	atomic_bool in_use;
};

struct fwstate_fuzzing_params {
	struct module *module;	     /**< Pointer to the module being tested */
	struct cp_module *cp_module; /**< Module configuration */

	void *arena;
	void *payload_arena;
	uint8_t *sync_arena;
	struct block_allocator ba;
	struct memory_context mctx;

	struct sync_packet_slot sync_packets[MAX_SYNC_PACKETS];
};

static struct fwstate_fuzzing_params fuzz_params = {
	.cp_module = NULL,
};

// Mock worker_packet_alloc
struct packet *
worker_packet_alloc(struct dp_worker *worker) {
	(void)worker;

	// Find first unused slot atomically
	for (size_t i = 0; i < MAX_SYNC_PACKETS; i++) {
		bool expected = false;
		if (atomic_compare_exchange_strong(
			    &fuzz_params.sync_packets[i].in_use, &expected, true
		    )) {
			struct rte_mbuf *m = fuzz_params.sync_packets[i].mbuf;
			struct packet_data empty = {
				.data = NULL,
				.size = 0,
				.rx_device_id = 0,
				.tx_device_id = 0,
			};
			init_mbuf(m, &empty, MBUF_MAX_SIZE);

			struct packet *p = mbuf_to_packet(m);
			memset(p, 0, sizeof(struct packet));
			p->mbuf = m;

			return p;
		}
	}

	return NULL;
}

// Mock worker_packet_free
void
worker_packet_free(struct packet *packet) {
	if (!packet)
		return;

	struct rte_mbuf *m = packet_to_mbuf(packet);

	for (size_t i = 0; i < MAX_SYNC_PACKETS; i++) {
		if (fuzz_params.sync_packets[i].mbuf == m) {
			atomic_store(
				&fuzz_params.sync_packets[i].in_use, false
			);
			return;
		}
	}
}

// Reset all sync packet slots
static void
reset_sync_packets(void) {
	for (size_t i = 0; i < MAX_SYNC_PACKETS; i++) {
		atomic_store(&fuzz_params.sync_packets[i].in_use, false);
	}
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

	// Create fw4state and fw6state maps
	fwmap_config_t fw4config = {
		.key_size = sizeof(struct fw4_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = 1,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW4,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW4,
		.copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE,
		.merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE,
		.index_size = 1024,
		.extra_bucket_count = 64,
	};
	fwmap_t *fw4state =
		fwmap_new(&fw4config, &config->cp_module.memory_context);
	if (!fw4state) {
		return -ENOMEM;
	}
	SET_OFFSET_OF(&config->cfg.fw4state, fw4state);

	fwmap_config_t fw6config = {
		.key_size = sizeof(struct fw6_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = 1,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW6,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW6,
		.copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE,
		.merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE,
		.index_size = 1024,
		.extra_bucket_count = 64,
	};
	fwmap_t *fw6state =
		fwmap_new(&fw6config, &config->cp_module.memory_context);
	if (!fw6state) {
		return -ENOMEM;
	}
	SET_OFFSET_OF(&config->cfg.fw6state, fw6state);

	// Configure sync settings
	uint8_t multicast_addr[16] = {
		0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01
	};
	memcpy(config->cfg.sync_config.dst_addr_multicast, multicast_addr, 16);
	config->cfg.sync_config.port_multicast = rte_cpu_to_be_16(9999);

	// Set imeouts
	config->cfg.sync_config.timeouts.tcp_syn_ack = 120000000000ULL;
	config->cfg.sync_config.timeouts.tcp_syn = 120000000000ULL;
	config->cfg.sync_config.timeouts.tcp_fin = 120000000000ULL;
	config->cfg.sync_config.timeouts.tcp = 120000000000ULL;
	config->cfg.sync_config.timeouts.udp = 30000000000ULL;
	config->cfg.sync_config.timeouts.default_ = 16000000000ULL;

	*cp_module = (struct cp_module *)config;
	return 0;
}

static int
fuzz_setup() {
	fuzz_params.arena = malloc(ARENA_SIZE);
	if (fuzz_params.arena == NULL) {
		return EXIT_FAILURE;
	}

	block_allocator_init(&fuzz_params.ba);
	block_allocator_put_arena(
		&fuzz_params.ba, fuzz_params.arena, ARENA_SIZE
	);

	memory_context_init(
		&fuzz_params.mctx, "fwstate fuzzing", &fuzz_params.ba
	);

	fuzz_params.module = new_module_fwstate();
	fuzz_params.payload_arena = memory_balloc(
		&fuzz_params.mctx,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4
	);
	if (fuzz_params.payload_arena == NULL) {
		return -ENOMEM;
	}

	// Allocate arena for sync packets
	fuzz_params.sync_arena = memory_balloc(
		&fuzz_params.mctx, MBUF_MAX_SIZE * MAX_SYNC_PACKETS
	);
	if (fuzz_params.sync_arena == NULL) {
		return -ENOMEM;
	}

	// Initialize sync packet slots
	for (size_t i = 0; i < MAX_SYNC_PACKETS; i++) {
		fuzz_params.sync_packets[i].mbuf =
			(struct rte_mbuf *)(fuzz_params.sync_arena +
					    i * MBUF_MAX_SIZE);
		fuzz_params.sync_packets[i].in_use = false;
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

	// Reset sync packets before processing
	reset_sync_packets();

	struct packet_data payload = {0};
	uint8_t packet_buffer[MBUF_MAX_SIZE];

	// If input size is a multiple of sync frame size (56 bytes),
	// wrap it as a valid sync packet to test sync processing paths
	if (size > 0 && size % sizeof(struct fw_state_sync_frame) == 0 &&
	    size <= 512) { // Reasonable limit for sync frames
		const size_t hdr_size = sizeof(struct rte_ether_hdr) +
					sizeof(struct rte_vlan_hdr) +
					sizeof(struct rte_ipv6_hdr) +
					sizeof(struct rte_udp_hdr);

		build_sync_packet(packet_buffer, data, size);
		payload.data = packet_buffer;
		payload.size = hdr_size + size;
	} else {
		// Use raw fuzzer input for other packet types
		payload.data = data;
		payload.size = size;
	}

	struct packet_front pf;
	packet_front_init(&pf);
	fill_packet_list_arena(
		&pf.input,
		1,
		&payload,
		MBUF_MAX_SIZE,
		fuzz_params.payload_arena,
		MBUF_MAX_SIZE * 4
	);
	parse_packet(pf.input.first);
	struct module_ectx module_ectx;
	SET_OFFSET_OF(&module_ectx.cp_module, fuzz_params.cp_module);

	// Create a minimal dp_worker structure for fuzzing
	struct dp_worker worker = {.idx = 0};

	// Process packet through fwstate module
	fuzz_params.module->handler(&worker, &module_ectx, &pf);

	// Free any sync packets that were added to output
	struct packet *packet;
	while ((packet = packet_list_pop(&pf.output)) != NULL) {
		worker_packet_free(packet);
	}

	return 0;
}
