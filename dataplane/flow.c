#include "flow.h"

#include <netinet/in.h>
#include <stdio.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ethdev.h>
#include <rte_ether.h>
#include <rte_flow.h>
#include <rte_ip.h>

#include "lib/logging/log.h"

// Router solicitation through neighbour advertisement.
#define FLOW_ND_TYPE_FIRST 133
#define FLOW_ND_TYPE_LAST 136
#define FLOW_DSCP_COUNT 64
// The two ECN bits below DSCP in the traffic class octet.
#define FLOW_ECN_BITS 2

// One rule's pattern storage: the items and the specs and masks they
// point at, kept together so a pattern never refers to freed stack.
struct flow_pattern {
	struct rte_flow_item items[5];
	uint32_t count;

	struct rte_flow_item_eth eth_spec;
	struct rte_flow_item_eth eth_mask;
	struct rte_flow_item_vlan vlan_spec;
	struct rte_flow_item_vlan vlan_mask;
	struct rte_flow_item_ipv4 ipv4_spec;
	struct rte_flow_item_ipv4 ipv4_mask;
	struct rte_flow_item_ipv6 ipv6_spec;
	struct rte_flow_item_ipv6 ipv6_mask;
	struct rte_flow_item_icmp6 icmp6_spec;
	struct rte_flow_item_icmp6 icmp6_mask;
};

static void
flow_pattern_push(
	struct flow_pattern *pattern,
	enum rte_flow_item_type type,
	const void *spec,
	const void *mask
) {
	pattern->items[pattern->count++] = (struct rte_flow_item){
		.type = type,
		.spec = spec,
		.mask = mask,
	};
}

// Start a pattern with the L2 header, optionally followed by one VLAN tag
// carrying the given inner ethertype.
//
// Without a tag the outer ethertype is matched instead, when one is given.
static void
flow_pattern_l2(struct flow_pattern *pattern, int tagged, uint16_t ether_type) {
	memset(pattern, 0, sizeof(*pattern));
	if (!tagged) {
		if (ether_type != 0) {
			pattern->eth_spec.hdr.ether_type =
				rte_cpu_to_be_16(ether_type);
			pattern->eth_mask.hdr.ether_type = UINT16_MAX;
		}
		flow_pattern_push(
			pattern,
			RTE_FLOW_ITEM_TYPE_ETH,
			&pattern->eth_spec,
			&pattern->eth_mask
		);
		return;
	}

	flow_pattern_push(pattern, RTE_FLOW_ITEM_TYPE_ETH, NULL, NULL);
	if (ether_type != 0) {
		pattern->vlan_spec.hdr.eth_proto = rte_cpu_to_be_16(ether_type);
		pattern->vlan_mask.hdr.eth_proto = UINT16_MAX;
	}
	flow_pattern_push(
		pattern,
		RTE_FLOW_ITEM_TYPE_VLAN,
		&pattern->vlan_spec,
		&pattern->vlan_mask
	);
}

static void
flow_pattern_end(struct flow_pattern *pattern) {
	flow_pattern_push(pattern, RTE_FLOW_ITEM_TYPE_END, NULL, NULL);
}

// Where a class's rules deliver: one queue, or several through an RSS
// action.
struct flow_target {
	uint16_t port_id;
	uint64_t rss_types;
	uint16_t queue_count;
	uint16_t *queues;
};

// Create one rule delivering its matches to the target, logging the
// driver's reason when it rejects the rule or has no flow API.
static int
flow_create(
	struct flow_target *target,
	const struct flow_pattern *pattern,
	const char *what
) {
	const struct rte_flow_attr attr = {.ingress = 1};
	const struct rte_flow_action_queue queue = {
		.index = target->queues[0],
	};
	const struct rte_flow_action_rss rss = {
		.func = RTE_ETH_HASH_FUNCTION_DEFAULT,
		.types = target->rss_types,
		.queue_num = target->queue_count,
		.queue = target->queues,
	};
	struct rte_flow_action actions[] = {
		{.type = RTE_FLOW_ACTION_TYPE_QUEUE, .conf = &queue},
		{.type = RTE_FLOW_ACTION_TYPE_END},
	};
	if (target->queue_count > 1) {
		actions[0].type = RTE_FLOW_ACTION_TYPE_RSS;
		actions[0].conf = &rss;
	}

	struct rte_flow_error error;
	memset(&error, 0, sizeof(error));
	if (rte_flow_create(
		    target->port_id, &attr, pattern->items, actions, &error
	    ) == NULL) {
		LOG(ERROR,
		    "port id %u: failed to install the steering rule for %s: "
		    "%s",
		    target->port_id,
		    what,
		    error.message != NULL ? error.message : "unknown error");
		return -1;
	}
	return 0;
}

// The IEEE 802.1 link-local block 01:80:c2:00:00:00-0f, matched by
// destination MAC whatever the ethertype. These frames are untagged.
static int
flow_steer_l2_control(struct flow_target *target) {
	static const struct rte_ether_addr block = {
		.addr_bytes = {0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}
	};
	static const struct rte_ether_addr block_mask = {
		.addr_bytes = {0xff, 0xff, 0xff, 0xff, 0xff, 0xf0}
	};

	struct flow_pattern pattern;
	flow_pattern_l2(&pattern, 0, 0);
	pattern.eth_spec.hdr.dst_addr = block;
	pattern.eth_mask.hdr.dst_addr = block_mask;
	flow_pattern_end(&pattern);
	return flow_create(target, &pattern, "l2_control");
}

static int
flow_steer_arp(struct flow_target *target) {
	for (int tagged = 0; tagged <= 1; ++tagged) {
		struct flow_pattern pattern;
		flow_pattern_l2(&pattern, tagged, RTE_ETHER_TYPE_ARP);
		flow_pattern_end(&pattern);
		int rc = flow_create(target, &pattern, "arp");
		if (rc != 0) {
			return rc;
		}
	}
	return 0;
}

static int
flow_rule_nd(struct flow_target *target, int tagged, uint8_t icmp6_type) {
	struct flow_pattern pattern;
	flow_pattern_l2(&pattern, tagged, tagged ? RTE_ETHER_TYPE_IPV6 : 0);
	pattern.ipv6_spec.hdr.proto = IPPROTO_ICMPV6;
	pattern.ipv6_mask.hdr.proto = UINT8_MAX;
	flow_pattern_push(
		&pattern,
		RTE_FLOW_ITEM_TYPE_IPV6,
		&pattern.ipv6_spec,
		&pattern.ipv6_mask
	);
	pattern.icmp6_spec.type = icmp6_type;
	pattern.icmp6_mask.type = UINT8_MAX;
	flow_pattern_push(
		&pattern,
		RTE_FLOW_ITEM_TYPE_ICMP6,
		&pattern.icmp6_spec,
		&pattern.icmp6_mask
	);
	flow_pattern_end(&pattern);
	return flow_create(target, &pattern, "nd");
}

static int
flow_steer_nd(struct flow_target *target) {
	for (int tagged = 0; tagged <= 1; ++tagged) {
		for (uint8_t type = FLOW_ND_TYPE_FIRST;
		     type <= FLOW_ND_TYPE_LAST;
		     ++type) {
			int rc = flow_rule_nd(target, tagged, type);
			if (rc != 0) {
				return rc;
			}
		}
	}
	return 0;
}

// Match a DSCP value in the IPv4 type of service or the IPv6 traffic
// class, ignoring the ECN bits.
static int
flow_rule_dscp(struct flow_target *target, int tagged, int ipv6, uint8_t dscp) {
	struct flow_pattern pattern;
	uint16_t ether_type = ipv6 ? RTE_ETHER_TYPE_IPV6 : RTE_ETHER_TYPE_IPV4;
	flow_pattern_l2(&pattern, tagged, tagged ? ether_type : 0);
	if (ipv6) {
		pattern.ipv6_spec.hdr.vtc_flow = rte_cpu_to_be_32(
			(uint32_t)dscp
			<< (RTE_IPV6_HDR_TC_SHIFT + FLOW_ECN_BITS)
		);
		pattern.ipv6_mask.hdr.vtc_flow =
			rte_cpu_to_be_32(RTE_IPV6_HDR_DSCP_MASK);
		flow_pattern_push(
			&pattern,
			RTE_FLOW_ITEM_TYPE_IPV6,
			&pattern.ipv6_spec,
			&pattern.ipv6_mask
		);
	} else {
		pattern.ipv4_spec.hdr.type_of_service =
			(uint8_t)(dscp << FLOW_ECN_BITS);
		pattern.ipv4_mask.hdr.type_of_service = RTE_IPV4_HDR_DSCP_MASK;
		flow_pattern_push(
			&pattern,
			RTE_FLOW_ITEM_TYPE_IPV4,
			&pattern.ipv4_spec,
			&pattern.ipv4_mask
		);
	}
	flow_pattern_end(&pattern);

	char what[16];
	snprintf(what, sizeof(what), "dscp %u", dscp);
	return flow_create(target, &pattern, what);
}

static int
flow_steer_dscp(struct flow_target *target, uint8_t dscp) {
	for (int tagged = 0; tagged <= 1; ++tagged) {
		for (int ipv6 = 0; ipv6 <= 1; ++ipv6) {
			int rc = flow_rule_dscp(target, tagged, ipv6, dscp);
			if (rc != 0) {
				return rc;
			}
		}
	}
	return 0;
}

enum flow_class {
	flow_class_l2_control,
	flow_class_arp,
	flow_class_nd,
	flow_class_dscp,
};

// Point the target at the queues listing a class, the DSCP value picking
// one for the DSCP class, and return whether any does.
static int
flow_select(
	struct flow_target *target,
	const struct dataplane_queue_role *roles,
	uint16_t queue_count,
	enum flow_class class,
	uint8_t dscp
) {
	target->queue_count = 0;
	for (uint16_t idx = 0; idx < queue_count; ++idx) {
		const struct dataplane_queue_role *role = roles + idx;
		int lists = 0;
		switch (class) {
		case flow_class_l2_control:
			lists = role->l2_control;
			break;
		case flow_class_arp:
			lists = role->arp;
			break;
		case flow_class_nd:
			lists = role->nd;
			break;
		case flow_class_dscp:
			lists = (role->dscp >> dscp) & 1;
			break;
		}
		if (lists) {
			target->queues[target->queue_count++] = idx;
		}
	}
	return target->queue_count != 0;
}

int
dataplane_flow_steer(
	uint16_t port_id,
	uint64_t rss_hash,
	const struct dataplane_queue_role *roles,
	uint16_t queue_count
) {
	uint16_t queues[queue_count];
	struct flow_target target = {
		.port_id = port_id,
		.rss_types = rss_hash != 0 ? rss_hash : RTE_ETH_RSS_IP,
		.queues = queues,
	};

	int rc = 0;
	if (flow_select(
		    &target, roles, queue_count, flow_class_l2_control, 0
	    )) {
		rc = flow_steer_l2_control(&target);
	}
	if (rc == 0 &&
	    flow_select(&target, roles, queue_count, flow_class_arp, 0)) {
		rc = flow_steer_arp(&target);
	}
	if (rc == 0 &&
	    flow_select(&target, roles, queue_count, flow_class_nd, 0)) {
		rc = flow_steer_nd(&target);
	}
	for (uint8_t value = 0; rc == 0 && value < FLOW_DSCP_COUNT; ++value) {
		if (flow_select(
			    &target, roles, queue_count, flow_class_dscp, value
		    )) {
			rc = flow_steer_dscp(&target, value);
		}
	}
	if (rc == 0) {
		return 0;
	}

	struct rte_flow_error error;
	memset(&error, 0, sizeof(error));
	rte_flow_flush(port_id, &error);
	return -1;
}
