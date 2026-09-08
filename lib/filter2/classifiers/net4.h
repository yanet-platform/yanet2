#pragma once

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/value.h"
#include "lib/filter2/filter.h"

/*
 * The region table beside the trie maps a raw trie region id to its
 * compacted value for multi-attribute filters, or directly to the rule
 * index for a single-attribute filter. Keeping the mapping in the table
 * rather than baking it into the trie preserves the no-match sentinel,
 * which the trie value encoding cannot represent.
 */
struct filter_query_attr_net4 {
	struct filter_query_attr attr;
	struct lpm lpm;
	struct vline line;
};

static inline void
filter_query_attr_net4_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_net4 *net4_attr =
		container_of(attr, struct filter_query_attr_net4, attr);

	lpm_free(&net4_attr->lpm);
	vline_free(&net4_attr->line);
	memory_bfree(
		memory_context, net4_attr, sizeof(struct filter_query_attr_net4)
	);
}
