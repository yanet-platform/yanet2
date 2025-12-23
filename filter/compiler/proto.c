#include "filter/classifiers/proto.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

#define TCP_FLAGS 9

////////////////////////////////////////////////////////////////////////////////

static int
proto_classifier_init_internal(
	struct value_registry *registry,
	struct proto_classifier *c,
	const struct filter_rule *rules,
	uint32_t rule_count,
	struct memory_context *mem
) {
	int res = value_table_init(&c->tcp_flags, mem, 1, 1 << TCP_FLAGS);
	if (res < 0) {
		return res;
	}
	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		const struct filter_proto *proto = &r->transport.proto;
		if (proto->proto != IPPROTO_TCP) { // not TCP
			continue;
		}
		if (proto->disable_bits & proto->enable_bits) {
			// impossible
			continue;
		}
		value_table_new_gen(&c->tcp_flags);
		int16_t mask = proto->disable_bits ^ ((1 << TCP_FLAGS) - 1) ^
			       proto->enable_bits;
		for (int16_t m = mask; m > 0; m = (m - 1) & mask) {
			value_table_touch(
				&c->tcp_flags, 0, m | proto->enable_bits
			);
		}
		value_table_touch(&c->tcp_flags, 0, proto->enable_bits);
	}

	value_table_compact(&c->tcp_flags);
	c->max_tcp_class = 0;
	for (uint16_t i = 0; i < (1 << TCP_FLAGS); ++i) {
		uint32_t value = value_table_get(&c->tcp_flags, 0, i);
		if (value > c->max_tcp_class) {
			c->max_tcp_class = value;
		}
	}

	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		const struct filter_proto *proto = &r->transport.proto;
		value_registry_start(registry);
		switch (proto->proto) {
		case IPPROTO_UDP:
			value_registry_collect(registry, c->max_tcp_class + 1);
			break;
		case IPPROTO_ICMP:
			value_registry_collect(registry, c->max_tcp_class + 2);
			break;
		case IPPROTO_TCP:
			if (proto->enable_bits & proto->disable_bits) {
				continue;
			}
			int16_t mask = proto->disable_bits ^
				       ((1 << TCP_FLAGS) - 1) ^
				       proto->enable_bits;
			for (int16_t m = mask; m > 0; m = (m - 1) & mask) {
				uint32_t value = value_table_get(
					&c->tcp_flags, 0, m | proto->enable_bits
				);
				value_registry_collect(registry, value);
			}
			uint32_t value = value_table_get(
				&c->tcp_flags, 0, proto->enable_bits
			);
			value_registry_collect(registry, value);
			break;
		case PROTO_UNSPEC:
			// all classifiers are suitable
			for (uint32_t class = 0; class <= c->max_tcp_class + 2;
			     ++class) {
				value_registry_collect(registry, class);
			}
			break;
		default:
			// TODO
			assert(0);
		}
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	struct proto_classifier *c =
		memory_balloc(memory_context, sizeof(struct proto_classifier));
	SET_OFFSET_OF(data, c);
	return proto_classifier_init_internal(
		registry, c, rules, rule_count, memory_context
	);
}

////////////////////////////////////////////////////////////////////////////////

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto)(
	void *data, struct memory_context *memory_context
) {
	struct proto_classifier *c = (struct proto_classifier *)data;
	value_table_free(&c->tcp_flags);
	memory_bfree(memory_context, c, sizeof(*c));
}