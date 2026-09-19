// Classic pcap capture loader of the acl module benchmarks: each
// record is wrapped into a standalone mbuf and run through the
// production packet parser, so the benchmarked lookups read exactly the
// fields the dataplane would set.
//
// The device id comes from the frame vlan tag mapped through the
// devices of the dumped ruleset in first seen order; adjust the table
// for a capture of a different ruleset.

#pragma once

// Loads a classic pcap capture into packet structs: each record is
// wrapped into a standalone mbuf and run through the production packet
// parser, so the query paths read exactly the fields the dataplane
// would set.
//
// The device id comes from the frame vlan tag, mapped to the ids the
// ruleset dump assigned to the acl.in device names in first seen order.

#include "lib/dataplane/packet/packet.h"
#include "lib/utils/packet.h"

#include <rte_ether.h>
#include <rte_mbuf.h>

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct bench_capture {
	struct packet *packets;
	struct packet **ptrs;
	uint32_t count;
	uint32_t parse_failures;
};

// Network family of a parsed packet, mirroring the production dispatch:
// the acl and forward dataplanes feed a packet into the ip6 batch only
// for an IPv6 ethertype, and into the port scoped batch only for an
// offset zero TCP or UDP transport.
static inline int
bench_packet_is_ip6(const struct packet *packet) {
	return packet->network_header.type ==
	       rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
}

static inline int
bench_packet_is_ip6_port(const struct packet *packet) {
	if (!bench_packet_is_ip6(packet)) {
		return 0;
	}
	return packet->fragment_offset == 0 &&
	       (packet->transport_header.type == IPPROTO_TCP ||
		packet->transport_header.type == IPPROTO_UDP);
}

static uint16_t
bench_vlan_device(uint16_t vlan) {
	switch (vlan) {
	case 1600:
		return 0;
	case 1619:
		return 1;
	case 2000:
		return 2;
	case 802:
		return 3;
	default:
		return 0;
	}
}

static int
bench_pcap_load(const char *path, struct bench_capture *cap) {
	FILE *f = fopen(path, "rb");
	if (f == NULL) {
		perror("fopen pcap");
		return -1;
	}

	uint8_t magic[4];
	if (fread(magic, 1, 4, f) != 4 ||
	    memcmp(magic, "\xd4\xc3\xb2\xa1", 4) != 0) {
		fprintf(stderr, "not a little endian classic pcap\n");
		return -1;
	}
	if (fseek(f, 20, SEEK_CUR) != 0) {
		return -1;
	}

	uint32_t alloc = 0;
	for (;;) {
		uint8_t rec[16];
		if (fread(rec, 1, 16, f) != 16) {
			break;
		}
		uint32_t caplen;
		memcpy(&caplen, rec + 8, 4);
		if (fseek(f, caplen, SEEK_CUR) != 0) {
			break;
		}
		++alloc;
	}
	rewind(f);
	if (fseek(f, 24, SEEK_SET) != 0) {
		return -1;
	}

	cap->packets = calloc(alloc ? alloc : 1, sizeof(*cap->packets));
	cap->ptrs = calloc(alloc ? alloc : 1, sizeof(*cap->ptrs));
	if (cap->packets == NULL || cap->ptrs == NULL) {
		return -1;
	}
	cap->count = 0;
	cap->parse_failures = 0;

	for (uint32_t rec_idx = 0; rec_idx < alloc; ++rec_idx) {
		uint8_t rec[16];
		if (fread(rec, 1, 16, f) != 16) {
			break;
		}
		uint32_t caplen;
		memcpy(&caplen, rec + 8, 4);

		size_t total =
			sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + caplen;
		total = (total + 63) & ~(size_t)63;
		struct rte_mbuf *mbuf = aligned_alloc(64, total);
		if (mbuf == NULL) {
			return -1;
		}
		memset(mbuf, 0, sizeof(*mbuf));
		mbuf->refcnt = 1;
		mbuf->buf_addr = ((char *)mbuf) + sizeof(*mbuf);
		mbuf->data_off = RTE_PKTMBUF_HEADROOM;
		mbuf->buf_len = RTE_PKTMBUF_HEADROOM + caplen;
		mbuf->data_len = caplen;
		mbuf->pkt_len = caplen;

		if (fread(rte_pktmbuf_mtod(mbuf, void *), 1, caplen, f) !=
		    caplen) {
			free(mbuf);
			break;
		}

		struct packet *packet = &cap->packets[cap->count];
		memset(packet, 0, sizeof(*packet));
		packet->mbuf = mbuf;
		if (parse_packet(packet) != 0) {
			++cap->parse_failures;
		}
		packet->module_device_id = bench_vlan_device(packet->vlan);
		cap->ptrs[cap->count] = packet;
		++cap->count;
	}

	fclose(f);
	return 0;
}

static void
bench_capture_free(struct bench_capture *cap) {
	for (uint32_t idx = 0; idx < cap->count; ++idx) {
		free_packet(&cap->packets[idx]);
	}
	free(cap->packets);
	free(cap->ptrs);
}

static uint64_t
bench_fnv1a(const uint32_t *values, uint32_t count) {
	uint64_t hash = 1469598103934665603ull;
	for (uint32_t idx = 0; idx < count; ++idx) {
		for (int byte = 0; byte < 4; ++byte) {
			hash ^= (values[idx] >> (byte * 8)) & 0xff;
			hash *= 1099511628211ull;
		}
	}
	return hash;
}
