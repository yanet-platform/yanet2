#pragma once

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

#include <rte_acl.h>
#include <rte_config.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/network.h"
#include "filter/rule.h"

////////////////////////////////////////////////////////////////////////////////

#define MAX_RULE_NUM 10000

////////////////////////////////////////////////////////////////////////////////

#define IP_HDR_OFFSET                                                          \
	(RTE_PKTMBUF_HEADROOM + sizeof(struct rte_mbuf) +                      \
	 sizeof(struct rte_ether_hdr))
#define TRANSPORT_HDR_OFFSET (IP_HDR_OFFSET + sizeof(struct rte_ipv4_hdr))

static_assert(
	IP_HDR_OFFSET == 398, "offset of the IP header calculated incorrect"
);

////////////////////////////////////////////////////////////////////////////////

static const struct rte_acl_field_def dpdk_acl_field_defs[5] = {
	{
		.type = RTE_ACL_FIELD_TYPE_BITMASK,
		.size = sizeof(uint8_t),
		.field_index = 0,
		.input_index = 0,
		.offset = IP_HDR_OFFSET +
			  offsetof(struct rte_ipv4_hdr, next_proto_id),
	},
	{.type = RTE_ACL_FIELD_TYPE_MASK,
	 .size = sizeof(uint32_t),
	 .field_index = 1,
	 .input_index = 1,
	 .offset = IP_HDR_OFFSET + offsetof(struct rte_ipv4_hdr, src_addr)},
	{.type = RTE_ACL_FIELD_TYPE_MASK,
	 .size = sizeof(uint32_t),
	 .field_index = 2,
	 .input_index = 2,
	 .offset = IP_HDR_OFFSET + offsetof(struct rte_ipv4_hdr, dst_addr)},
	{.type = RTE_ACL_FIELD_TYPE_RANGE,
	 .size = sizeof(uint16_t),
	 .field_index = 3,
	 .input_index = 3,

	 // both udp and tcp headers start with src port following dst port
	 .offset = TRANSPORT_HDR_OFFSET},
	{.type = RTE_ACL_FIELD_TYPE_RANGE,
	 .size = sizeof(uint16_t),
	 .field_index = 4,
	 .input_index = 4,
	 .offset = TRANSPORT_HDR_OFFSET + sizeof(uint16_t)}
};

////////////////////////////////////////////////////////////////////////////////

RTE_ACL_RULE_DEF(dpdk_acl_rule, RTE_DIM(dpdk_acl_field_defs));

////////////////////////////////////////////////////////////////////////////////

static const struct rte_acl_param dpdk_acl_params = {
	.name = "DPDK_ACL",
	.socket_id = SOCKET_ID_ANY,
	.rule_size = RTE_ACL_RULE_SZ(RTE_DIM(dpdk_acl_field_defs)),

	// maximum number of rules
	.max_rule_num = MAX_RULE_NUM,
};

////////////////////////////////////////////////////////////////////////////////

struct dpdk_acl {
	struct dpdk_acl_rule rules[MAX_RULE_NUM];
	struct rte_acl_ctx *ctx;
	struct rte_acl_config cfg;
};

////////////////////////////////////////////////////////////////////////////////

struct filter_rule_holder {
	struct net4 nets_src[MAX_RULE_NUM];
	struct net4 nets_dst[MAX_RULE_NUM];
	struct filter_port_range ports_src[MAX_RULE_NUM];
	struct filter_port_range ports_dst[MAX_RULE_NUM];
	struct filter_rule rules[MAX_RULE_NUM];
};