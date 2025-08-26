#include "helper.h"
#include "common/exp_array.h"
#include "common/memory.h"
#include "common/registry.h"
#include <stdlib.h>
#include <string.h>

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
) {
	int res = value_registry_init(registry, memory_context);
	if (res < 0) {
		return res;
	}
	for (uint32_t i = 0; i < actions; ++i) {
		res = value_registry_start(registry);
		if (res < 0) {
			return res;
		}
		res = value_registry_collect(registry, 0);
		if (res < 0) {
			return res;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

struct value_set_ctx {
	const struct filter_rule *actions;
	struct value_table *table;
	struct value_registry *registry;
};

static int
action_list_is_term(struct value_registry *registry, uint32_t range_idx) {
	struct value_range *range = ADDR_OF(&registry->ranges) + range_idx;
	if (range->count == 0)
		return 0;

	uint32_t action_id =
		ADDR_OF(&registry->values)[range->from + range->count - 1];
	return !(action_id & ACTION_NON_TERMINATE);
}

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t prev_value = value_table_get(set_ctx->table, v1, v2);

	if (!action_list_is_term(set_ctx->registry, prev_value)) {
		/*
		 * FIXME: we assume value table produces increasing sequence
		 * of values - this is important for value registry handling.
		 */
		int res = value_table_touch(set_ctx->table, v1, v2);

		if (res <= 0)
			return res;

		if (value_registry_start(set_ctx->registry))
			return -1;

		struct value_range *copy_range =
			ADDR_OF(&set_ctx->registry->ranges) + prev_value;

		for (uint32_t ridx = copy_range->from;
		     ridx < copy_range->from + copy_range->count;
		     ++ridx) {
			value_registry_collect(
				set_ctx->registry,
				ADDR_OF(&set_ctx->registry->values)[ridx]
			);
		}

		value_registry_collect(
			set_ctx->registry, set_ctx->actions[idx].action
		);
	}

	return 0;
}

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table,
		    memory_context,
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	if (value_registry_init(registry, memory_context)) {
		goto error_registry;
	}

	if (value_registry_start(registry))
		return -1;

	struct value_set_ctx set_ctx;
	set_ctx.actions = actions;
	set_ctx.table = table;
	set_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    ))
			goto error_merge;
	}

	return 0;

error_merge:
	value_registry_free(registry);

error_registry:
	value_table_free(table);

	return 0;
}

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_table *table = (struct value_table *)data;
	if (value_table_touch(table, v1, v2) < 0)
		return -1;
	return 0;
}

static int
merge_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
) {
	int ret = value_table_init(
		table,
		memory_context,
		value_registry_capacity(registry1),
		value_registry_capacity(registry2)
	);
	if (ret < 0) {
		return -1;
	}

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		ret = value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			table
		);
		if (ret < 0) {
			return -1;
		}
	}

	value_table_compact(table);

	return 0;
}

struct value_collect_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static int
value_table_collect_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_collect_ctx *collect_ctx =
		(struct value_collect_ctx *)data;
	return value_registry_collect(
		collect_ctx->registry,
		value_table_get(collect_ctx->table, v1, v2)
	);
}

static int
collect_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	int ret = value_registry_init(registry, memory_context);

	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		ret = value_registry_start(registry);
		if (ret < 0) {
			return -1;
		}
		ret = value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_collect_action,
			&collect_ctx
		);
		if (ret < 0) {
			return -1;
		}
	}

	return 0;
}

int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (merge_registry_values(
		    memory_context, registry1, registry2, table
	    )) {
		return -1;
	}

	if (collect_registry_values(
		    memory_context, registry1, registry2, table, registry
	    )) {
		value_table_free(table);
		return -1;
	}

	return 0;
}

int
on_found_rule_classifier(const struct trie_vertex *v, void *data) {
	struct rule_classifiers *cls = data;
	for (uint64_t i = 0; i < v->rule_count; ++i) {
		uint32_t rule = v->rules[i];
		if (cls->classifiers[rule] != NULL &&
		    cls->classifiers[rule][cls->count[rule] - 1] ==
			    v->classifier) {
			continue;
		}
		void *classifiers = cls->classifiers[rule];
		int ret = mem_array_expand_exp(
			cls->mctx,
			&classifiers,
			sizeof(uint32_t),
			&cls->count[rule]
		);
		if (ret < 0) {
			return ret;
		}
		cls->classifiers[rule] = classifiers;
		cls->classifiers[rule][cls->count[rule] - 1] = v->classifier;
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
compare_ints(const void *a, const void *b) {
	uint32_t arg1 = *(const uint32_t *)a;
	uint32_t arg2 = *(const uint32_t *)b;
	return (int)(arg1 > arg2) - (int)(arg1 < arg2);
}

int
fill_rule_registry_by_trie(
	const struct trie *trie,
	uint32_t rule_count,
	struct value_registry *registry,
	struct memory_context *mctx
) {
	uint32_t **classifiers =
		memory_balloc(mctx, rule_count * sizeof(uint32_t *));
	if (classifiers == NULL) {
		return -1;
	}
	uint64_t *count = memory_balloc(mctx, rule_count * sizeof(uint64_t));
	if (count == NULL) {
		memory_bfree(
			mctx, classifiers, rule_count * sizeof(uint32_t *)
		);
		return -1;
	}

	for (uint32_t i = 0; i < rule_count; ++i) {
		classifiers[i] = NULL;
	}
	memset(count, 0, sizeof(uint64_t) * rule_count);

	struct rule_classifiers cls = {
		.classifiers = classifiers, .count = count, .mctx = mctx
	};

	int ret = trie_collect_rule_classifiers(trie, &cls);
	if (ret < 0) {
		goto free;
	}

	for (uint32_t rule = 0; rule < rule_count; ++rule) {
		ret = value_registry_start(registry);
		if (ret < 0) {
			goto free;
		}
		qsort(cls.classifiers[rule],
		      cls.count[rule],
		      sizeof(uint32_t),
		      compare_ints);
		for (uint32_t i = 0; i < cls.count[rule]; ++i) {
			uint32_t c = cls.classifiers[rule][i];
			if (i > 0 && c == cls.classifiers[rule][i - 1]) {
				continue;
			}
			ret = value_registry_collect(registry, c);
			if (ret < 0) {
				goto free;
			}
		}
	}

free:
	memory_bfree(mctx, classifiers, rule_count * sizeof(uint32_t *));
	memory_bfree(mctx, count, rule_count * sizeof(uint64_t));

	return ret;
}