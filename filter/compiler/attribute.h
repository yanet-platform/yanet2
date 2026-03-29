#pragma once

#include "common/memory.h"
#include "common/registry.h"

#include "filter/rule.h"

#include "declare.h"

#define REGISTER_ATTRIBUTE(name)                                               \
	int FILTER_ATTR_COMPILER_INIT_FUNC(name)(                              \
		struct value_registry * registry,                              \
		void **data,                                                   \
		const struct filter_rule *rules,                               \
		size_t rule_count,                                             \
		struct memory_context *mctx                                    \
	);                                                                     \
	void FILTER_ATTR_COMPILER_FREE_FUNC(name)(                             \
		void *data, struct memory_context *mctx                        \
	);

REGISTER_ATTRIBUTE(port_src);
REGISTER_ATTRIBUTE(port_dst);
REGISTER_ATTRIBUTE(port_fast_src);
REGISTER_ATTRIBUTE(port_fast_dst);
REGISTER_ATTRIBUTE(proto);
REGISTER_ATTRIBUTE(proto_range);
REGISTER_ATTRIBUTE(proto_range_fast);
REGISTER_ATTRIBUTE(net4_src);
REGISTER_ATTRIBUTE(net4_dst);
REGISTER_ATTRIBUTE(net6_src);
REGISTER_ATTRIBUTE(net6_dst);
REGISTER_ATTRIBUTE(vlan);
REGISTER_ATTRIBUTE(device);
REGISTER_ATTRIBUTE(net4_fast_dst);
REGISTER_ATTRIBUTE(net4_fast_src);
REGISTER_ATTRIBUTE(net6_fast_dst);
REGISTER_ATTRIBUTE(net6_fast_src);

#undef REGISTER_ATTRIBUTE