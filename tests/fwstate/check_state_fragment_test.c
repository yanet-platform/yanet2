/*
 * Firewall-state CHECK_STATE fragment guard regression test.
 *
 * Verifies that fwstate_check_state_table reports a miss without a sync
 * for a packet whose transport header is marked unavailable, while the
 * byte-identical untagged packet still finds state built from its real
 * transport header.
 */

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_udp.h>

#include "common/memory.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/fwstate/lookup.h"
#include "lib/fwstate/types.h"
#include "lib/statemap/fwtable.h"
#include "lib/utils/packet.h"
#include "test_utils.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE_MB 16
#define ARENA_SIZE (1 << 20) * ARENA_SIZE_MB

#define TEST_SRC_PORT 12345
#define TEST_DST_PORT 80
#define TEST_TTL_NS (120ULL * 1000 * 1000 * 1000)

static uint64_t now_time = 1000000;

// Builds eth + IPv4 + UDP, then parses the frame so the packet metadata
// matches what the dataplane hands to the state check.
static int
build_udp_packet(struct packet *pkt) {
	const uint16_t transport_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr);
	const uint16_t pkt_len =
		transport_offset + sizeof(struct rte_udp_hdr) + 4;
	memset(pkt, 0, sizeof(*pkt));
	pkt->mbuf = alloc_mbuf(128, pkt_len, 0);
	if (pkt->mbuf == NULL) {
		return -1;
	}

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(pkt->mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(pkt_len - sizeof(*eth));
	ip4->time_to_live = 64;
	ip4->next_proto_id = IPPROTO_UDP;
	const uint8_t src4addr[NET4_LEN] = {192, 0, 2, 1};
	const uint8_t dst4addr[NET4_LEN] = {10, 0, 0, 1};
	memcpy(&ip4->src_addr, src4addr, NET4_LEN);
	memcpy(&ip4->dst_addr, dst4addr, NET4_LEN);
	ip4->hdr_checksum = 0;
	ip4->hdr_checksum = rte_ipv4_cksum(ip4);

	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ip4 + 1);
	udp->src_port = rte_cpu_to_be_16(TEST_SRC_PORT);
	udp->dst_port = rte_cpu_to_be_16(TEST_DST_PORT);
	udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp) + 4);

	return parse_packet(pkt);
}

// Derives the state key exactly the way fwstate_build_state_key_v4 does, so
// the control lookup must hit.
static void
build_state_key(struct rte_ipv4_hdr *ip4, struct fw4_state_key *key) {
	memset(key, 0, sizeof(*key));
	key->hdr.proto = ip4->next_proto_id;
	key->src_addr = ip4->dst_addr;
	key->dst_addr = ip4->src_addr;

	struct rte_udp_hdr *udp =
		(struct rte_udp_hdr *)((uint8_t *)ip4 + sizeof(*ip4));
	key->hdr.src_port = rte_be_to_cpu_16(udp->dst_port);
	key->hdr.dst_port = rte_be_to_cpu_16(udp->src_port);
}

int
main(void) {
	void *arena =
		mmap(NULL,
		     ARENA_SIZE,
		     PROT_READ | PROT_WRITE,
		     MAP_PRIVATE | MAP_ANONYMOUS,
		     -1,
		     0);
	assert(arena != MAP_FAILED);

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "check_state_frag");

	fwmap_config_t config = {
		.key_size = sizeof(struct fw4_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0xdeadbeef,
		.worker_count = 1,
		.index_size = 128,
		.extra_bucket_count = 16,
	};

	fwtable_t table = {0};
	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);

	struct packet pkt;
	assert(build_udp_packet(&pkt) == 0);

	// Seed the state for the packet's tuple, the way the create-state
	// path would have stored it.
	struct rte_ipv4_hdr *ip4 = rte_pktmbuf_mtod_offset(
		pkt.mbuf, struct rte_ipv4_hdr *, pkt.network_header.offset
	);
	struct fw4_state_key key;
	build_state_key(ip4, &key);
	struct fw_state_value value = {0};
	value.updated_at = now_time;
	value.last_ttl = TEST_TTL_NS;
	assert(fwtable_insert(
		       &table, 0, now_time, TEST_TTL_NS, &key, &value, NULL
	       ) >= 0);

	// Control: an untagged packet finds the state through its real
	// transport header.
	enum sync_packet_direction sync = SYNC_EGRESS;
	bool found = fwstate_check_state_table(&table, &pkt, now_time, &sync);
	assert(found && "state must be found for the untagged packet");

	// The same packet with the transport header tagged unavailable must
	// miss without a sync: the lookup must not derive a key from bytes
	// that a fragment would carry as payload.
	pkt.transport_header.type |= PACKET_TRANSPORT_HEADER_UNAVAILABLE;
	sync = SYNC_EGRESS;
	found = fwstate_check_state_table(&table, &pkt, now_time, &sync);
	assert(!found && "a tagged packet must not find state");
	assert(sync == SYNC_NONE &&
	       "a tagged packet miss must not require a sync");

	free_packet(&pkt);

	fwmap_t *layer = ADDR_OF(&table.head);
	while (layer != NULL) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}
	memory_context_fini(ctx);
	munmap(arena, ARENA_SIZE);

	printf("check_state_fragment: PASSED\n");
	return 0;
}
