#pragma once

#include "common/memory.h"
#include <common/registry.h>
#include <common/value.h>

#include <filter/rule.h>

#include <lib/dataplane/packet/packet.h>

////////////////////////////////////////////////////////////////////////////////

#define PROTO_RANGE_CLASSIFIER_MAX_VALUE ((1 << 10))

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
	(void)packet;
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	uint16_t proto = packet->transport_header
				 .type; /// < get proto of the packet here
	return value_table_get(&c->table, 0, proto);
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