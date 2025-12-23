#pragma once

#include "common/memory.h"
#include "common/registry.h"

#include "filter/rule.h"

#include "declare.h"

typedef int (*filter_attr_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
);

typedef void (*filter_attr_free_func)(
	void *data, struct memory_context *memory_context
);

struct filter_attr_compiler {
	filter_attr_init_func init;
	filter_attr_free_func free;
};

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
	);                                                                     \
	static const struct filter_attr_compiler FILTER_ATTR_COMPILER(name     \
	) = {FILTER_ATTR_COMPILER_INIT_FUNC(name),                             \
	     FILTER_ATTR_COMPILER_FREE_FUNC(name)}

REGISTER_ATTRIBUTE(port_src);
REGISTER_ATTRIBUTE(port_dst);
REGISTER_ATTRIBUTE(proto);
REGISTER_ATTRIBUTE(proto_range);
REGISTER_ATTRIBUTE(net4_src);
REGISTER_ATTRIBUTE(net4_dst);
REGISTER_ATTRIBUTE(net6_src);
REGISTER_ATTRIBUTE(net6_dst);
REGISTER_ATTRIBUTE(vlan);
REGISTER_ATTRIBUTE(device);

#undef REGISTER_ATTRIBUTE