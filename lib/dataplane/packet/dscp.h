#pragma once

#include <stdint.h>

#define DSCP_MARK_NEVER 0
#define DSCP_MARK_DEFAULT 1
#define DSCP_MARK_ALWAYS 2

#define DSCP_MARK_MASK 0xFC
#define DSCP_MARK_SHIFT 2
#define DSCP_ECN_MASK 0x03

// #include <rte_ip.h>
struct rte_ipv4_hdr;
struct rte_ipv6_hdr;

struct dscp_config {
	uint8_t flag;
	uint8_t mark;
};

int
dscp_mark_v4(struct rte_ipv4_hdr *ip4_hdr, struct dscp_config config);

int
dscp_mark_v6(struct rte_ipv6_hdr *ip6_hdr, struct dscp_config config);
