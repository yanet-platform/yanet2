/**
 * @file rule.h
 * @brief Data structures describing filter rules and action encoding.
 *
 * A filter is built from an array of struct filter_rule. Each rule may specify:
 *  - L3 nets (IPv4 / IPv6) for source/destination
 *  - L4 transport constraints (proto ranges, TCP flags, port ranges)
 *  - Optional device and VLAN constraints
 *  - A 32-bit action: lower 15 bits are user action, bit 15 is terminate flag,
 *    high 16 bits form category mask (0 = applies to all categories).
 *
 * See also:
 *  - filter/compiler.h (filter_init/filter_free)
 *  - filter/query.h (FILTER_QUERY and post-processing helpers)
 */
#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "common/filter_views.h"
#include "common/network.h"

/*
 * The rule format of the legacy filter. The shared value views above
 * (nets, ports, protocols, devices, vlans, the fragmentation
 * constraint) moved to common/filter_views.h, shared with the
 * lib/classify attribute compilers; the structures below are the
 * format this library compiles its rule arrays from.
 */

struct filter_net6 {
	uint32_t src_count;
	uint32_t dst_count;

	struct net6 *srcs;
	struct net6 *dsts;
};

struct filter_net4 {
	uint32_t src_count;
	uint32_t dst_count;

	struct net4 *srcs;
	struct net4 *dsts;
};

struct filter_transport {
	uint16_t proto_count;
	struct filter_proto_range *protos;

	uint16_t src_count;
	struct filter_port_range *srcs;

	uint16_t dst_count;
	struct filter_port_range *dsts;
};

#define VLAN_UNSPEC ((uint16_t)-1)

/**
 * @brief A single classification rule.
 *
 * Fields used by different subsystems:
 *  - net6/net4: lists of source/destination networks (match if any applies)
 *  - transport: protocol/flag windows and port ranges
 *  - devices/VLAN: optional device and VLAN constraints
 *  - action (32 bits, layout):
 *      [31..16] category mask (0 => all categories)
 *      [15]     terminate bit (0 => terminal, 1 => non-terminate)
 *      [14..0]  user action (application-defined)
 */
struct filter_rule {
	struct filter_net6 net6;
	struct filter_net4 net4;
	struct filter_transport transport;
	uint16_t device_count;
	struct filter_device *devices;

	uint16_t vlan_range_count;
	struct filter_vlan_range *vlan_ranges;

	uint16_t vlan;

	enum filter_ip_fragment fragment;

	uint32_t action;
};
