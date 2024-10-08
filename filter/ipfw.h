#ifndef FILTER_IPFW_H
#define FILTER_IPFW_H

#include <stdint.h>

#include "dataplane/filter.h"

#include "lpm.h"

struct ipfw_net6 {
	uint64_t addr_hi;
	uint64_t addr_lo;
	uint64_t mask_hi;
	uint64_t mask_lo;
};

struct ipfw_net4 {
	uint32_t addr;
	uint32_t mask;
};

struct ipfw_net6_filter {
	uint32_t src_count;
	uint32_t dst_count;
	struct ipfw_net6 *srcs;
	struct ipfw_net6 *dsts;
};

struct ipfw_net4_filter {
	uint32_t src_count;
	uint32_t dst_count;
	struct ipfw_net4 *srcs;
	struct ipfw_net4 *dsts;
};

struct ipfw_port_range {
	uint16_t from;
	uint16_t to;
};

struct ipfw_transport_filter {
	uint16_t proto_flags;
	uint16_t src_count;
	uint16_t dst_count;
	struct ipfw_port_range *srcs;
	struct ipfw_port_range *dsts;
};

struct ipfw_filter {
	struct ipfw_net6_filter net6;
	struct ipfw_net4_filter net4;
	struct ipfw_transport_filter transport;
};

struct ipfw_filter_action {
	struct ipfw_filter filter;
	uint32_t action;
};

struct ipfw_packet_filter {
	struct filter filter;
	struct lpm64 src_net6_hi;
	struct lpm64 src_net6_lo;
	struct lpm64 dst_net6_hi;
	struct lpm64 dst_net6_lo;

	uint32_t src_port[65536];
	uint32_t dst_port[65536];

	uint16_t proto_flag[65536];

	filter_classify classify[6];
	struct filter_lookup lookups[5];
	struct filter_table tables[5];
};

int
ipfw_packet_filter_create(
	struct ipfw_filter_action *actions,
	uint32_t count,
	struct ipfw_packet_filter *filter);

#endif
