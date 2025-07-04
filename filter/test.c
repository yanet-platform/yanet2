#include "attribute.h"
#include "common/memory_block.h"
#include "dataplane/packet/packet.h"
#include "filter.h"

#include <rte_mbuf.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>

#include <assert.h>
#include <stdio.h>

struct rte_mbuf* make_mbuf(uint32_t src_ip, uint32_t dst_ip,
                        uint16_t src_port, uint16_t dst_port) {
    size_t total_size = sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + 2048;
    struct rte_mbuf* mbuf = malloc(total_size);
    
    if (!mbuf) return NULL;

	uint16_t total_len = sizeof(struct rte_ether_hdr) +
					sizeof(struct rte_ipv4_hdr) +
					sizeof(struct rte_udp_hdr);

	mbuf->buf_addr = ((char*)mbuf) + sizeof(struct rte_mbuf);
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->buf_len = 2048 + RTE_PKTMBUF_HEADROOM;

    mbuf->pkt_len = total_len;
	mbuf->l2_len = sizeof(struct rte_ether_hdr);
    mbuf->l3_len = sizeof(struct rte_ipv4_hdr);
    
	struct rte_ether_hdr* eth = rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr*);
    struct rte_ipv4_hdr* ip = (struct rte_ipv4_hdr*)(eth + 1);
    struct rte_udp_hdr* udp = (struct rte_udp_hdr*)(ip + 1);

	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

    ip->version_ihl = 0x45;
    ip->type_of_service = 0;
    ip->total_length = rte_cpu_to_be_16(total_len - sizeof(*eth));
    ip->packet_id = 0;
    ip->fragment_offset = 0;
    ip->time_to_live = 64;
    ip->next_proto_id = IPPROTO_UDP;
    ip->src_addr = rte_cpu_to_be_32(src_ip);
    ip->dst_addr = rte_cpu_to_be_32(dst_ip);
    ip->hdr_checksum = 0;

	udp->src_port = rte_cpu_to_be_16(src_port);
    udp->dst_port = rte_cpu_to_be_16(dst_port);
    udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
    udp->dgram_cksum = 0;

    return mbuf;
}

void free_mbuf(struct rte_mbuf *mbuf) {
	free(mbuf);
}

int
main() {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 24); // 16MB
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_attribute attributes[2] = {
		src_port_attribute, dst_port_attribute
	};

	struct filter_net6 dummy_net6 = {0, 0, NULL, NULL};
	struct filter_net4 dummy_net4 = {0, 0, NULL, NULL};

	struct filter_port_range src_port_range_1 = {5, 7};
	struct filter_port_range dst_port_range_1 = {1, 5};
	struct filter_transport f1 = {
		0, 1, 1, &src_port_range_1, &dst_port_range_1
	};

	struct filter_port_range src_port_range_2 = {6, 8};
	struct filter_port_range dst_port_range_2 = {3, 4};
	struct filter_transport f2 = {
		0, 1, 1, &src_port_range_2, &dst_port_range_2
	};

	struct filter_action actions[2] = {
		{dummy_net6, dummy_net4, f1, 1},
		{dummy_net6, dummy_net4, f2, 2},
	};

	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 2, &memory_context);
	assert(res == 0);

	// setup packet
	struct packet packet;

	uint32_t src_ip = RTE_IPV4(192, 168, 1, 1);
	uint32_t dst_ip = RTE_IPV4(192, 168, 1, 2);
	uint16_t src_port = 6;
	uint16_t dst_port = 3;

	packet.mbuf = make_mbuf(src_ip, dst_ip, src_port, dst_port);
	assert(packet.mbuf != NULL);
	int parse_result = parse_packet(&packet);
	assert(parse_result == 0);
	
	assert(packet_src_port(&packet) == 6);
	assert(packet_dst_port(&packet) == 3);

	uint32_t *result_actions;
	uint32_t result_count;
	res = filter_query(&filter, &packet, &result_actions, &result_count);
	assert(res == 0);

	assert(result_count == 1);
	assert(result_actions[0] == 1);

	puts("OK!");

	free_mbuf(packet.mbuf);

	return 0;
}