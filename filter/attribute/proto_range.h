#pragma once

#include "common/memory.h"
#include <common/registry.h>
#include <common/value.h>

#include <filter/rule.h>

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_tcp.h>
#include <rte_ether.h>
#include <rte_ip.h>

#include <lib/dataplane/packet/packet.h>

////////////////////////////////////////////////////////////////////////////////

#define PROTO_RANGE_CLASSIFIER_MAX_VALUE ((1 << 16))

////////////////////////////////////////////////////////////////////////////////

struct proto_range_classifier {
	struct value_table table;
};

static inline int
collect_proto_values(
	struct memory_context *memory_context,
	const struct filter_rule *rules,
	uint32_t count,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table, memory_context, 1, PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    ))
		return -1;

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {

		value_table_new_gen(table);

		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		size_t proto_count = rule->transport.proto_count;

		for (struct filter_proto_range *proto_range = proto_ranges;
		     proto_range < proto_ranges + proto_count;
		     ++proto_range) {
			for (uint32_t proto = proto_range->from;
			     proto <= proto_range->to;
			     ++proto) {
				value_table_touch(table, 0, proto);
			}
		}
	}

	value_table_compact(table);

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {
		value_registry_start(registry);

		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		size_t proto_count = rule->transport.proto_count;

		for (struct filter_proto_range *proto_range = proto_ranges;
		     proto_range < proto_ranges + proto_count;
		     ++proto_range) {
			for (uint32_t proto = proto_range->from;
			     proto <= proto_range->to;
			     ++proto) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, proto)
				);
			}
		}
	}

	return 0;
}

static inline int
proto_range_classifier_init(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *mctx
) {
	struct proto_range_classifier *classifier =
		memory_balloc(mctx, sizeof(struct proto_range_classifier));
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return collect_proto_values(
		mctx, rules, rule_count, &classifier->table, registry
	);
}

static inline uint32_t
proto_range_classifier_lookup(struct packet *packet, void *data) {
	struct proto_range_classifier *c = (struct proto_range_classifier *)data;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t protocol = packet->transport_header.type;
	uint8_t parameter = 0;
	uint8_t *proto_data = rte_pktmbuf_mtod_offset(mbuf, uint8_t *, packet->transport_header.offset);

	switch (protocol) {
	case IPPROTO_TCP: {
		struct rte_tcp_hdr *tcp_hdr = (struct rte_tcp_hdr *)proto_data;
		parameter = tcp_hdr->tcp_flags & 0xFF;
		break;
	}
	case IPPROTO_ICMP: {
		struct icmphdr *icmp_hdr = (struct icmphdr *)proto_data;
		parameter = icmp_hdr->type;
		break;
	}
	case IPPROTO_ICMPV6: {
		struct icmp6_hdr *icmp6_hdr = (struct icmp6_hdr *)proto_data;
		parameter = icmp6_hdr->icmp6_type;
		break;
	}
	default:
		parameter = 0;
		break;
	}

	uint16_t proto_combined = ((uint16_t)protocol << 8) | ((uint16_t)parameter);
	uint32_t action = value_table_get(&c->table, 0, proto_combined);

	return action;
}


static inline void
proto_range_classifier_free(void *data, struct memory_context *memory_context) {
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	value_table_free(&c->table);
	memory_bfree(memory_context, c, sizeof(*c));
}

////////////////////////////////////////////////////////////////////////////////

#undef PROTO_RANGE_CLASSIFIER_MAX_VALUE
