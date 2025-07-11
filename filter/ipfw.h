#pragma once

#include <stdint.h>

#include "rule.h"

#include "common/lpm.h"
#include "common/registry.h"

struct filter_compiler {
	struct memory_context memory_context;
	struct lpm src_net4;
	struct lpm dst_net4;
	struct value_table src_port4;
	struct value_table dst_port4;

	struct lpm src_net6_hi;
	struct lpm src_net6_lo;
	struct lpm dst_net6_hi;
	struct lpm dst_net6_lo;
	struct value_table src_port6;
	struct value_table dst_port6;

	struct {
		struct value_table network;
		struct value_table transport_port;
		struct value_table result;
		struct value_registry result_registry;
	} v4_lookups;

	struct {
		struct value_table network_hi;
		struct value_table network_lo;
		struct value_table network;
		struct value_table transport_port;
		struct value_table result;
		struct value_registry result_registry;
	} v6_lookups;
};

int
filter_compiler_init(
	struct filter_compiler *compiler,
	struct memory_context *memory_context,
	struct filter_rule *actions,
	uint32_t count
);
