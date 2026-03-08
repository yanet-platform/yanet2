#pragma once

#include "packet.h"

int
packet_ip4_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);

int
packet_ip6_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);

int
packet_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t s,
	uint8_t ttl
);

int
packet_encap_ip4_udp(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	const uint8_t *src_port,
	const uint8_t *dst_port
);

int
packet_encap_ip6_udp(
	struct packet *packet,
	const uint8_t *src_addr,
	const uint8_t *dst_addr,
	const uint8_t *src_port,
	const uint8_t *dst_port
);
