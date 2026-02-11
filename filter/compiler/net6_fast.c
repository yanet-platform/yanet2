#include "../classifiers/net6_fast.h"
#include "common/big_array.h"
#include "common/btree/u64.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/registry.h"
#include "declare.h"
#include "rule.h"
#include <assert.h>
#include <stdlib.h>

static int
validate_net6_half(uint8_t *bytes) {
	int prev = 1;
	for (int bit = 63; bit >= 0; --bit) {
		int byte = (63 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = bytes[byte] & (1 << inner_bit);
		if (!prev && cur) {
			return 0;
		}
		prev = cur != 0;
	}
	return 1;
}

struct net6_count {
	size_t count;
	struct net6 *nets;
};

typedef struct net6_count(get_net)(const struct filter_rule *);

static int
validate_net6(struct net6 *net6) {
	return validate_net6_half(net6->mask) &&
	       validate_net6_half(net6->mask + 8);
}

struct segment {
	uint64_t from;
	uint64_t to;
};

static int
compare_segments(const void *left_void, const void *right_void) {
	struct segment *left = (struct segment *)left_void;
	struct segment *right = (struct segment *)right_void;
	if (left->from < right->from) {
		return -1;
	} else if (left->from > right->from) {
		return 1;
	} else if (left->to > right->to) {
		return -1;
	} else if (left->to < right->to) {
		return 1;
	}
	return 0;
}

static void
net6_part_from_to(struct net6 *n, int part, uint64_t *from, uint64_t *to) {
	uint64_t addr = 0;
	uint64_t mask = 0;

	for (size_t i = 0; i < 8; ++i) {
		addr |= ((uint64_t)n->addr[i + 8 * part]) << (56 - i * 8);
		mask |= ((uint64_t)n->mask[i + 8 * part]) << (56 - i * 8);
	}

	*from = addr & mask;
	*to = addr | ~mask;
}

static int
validate_and_count(
	const struct filter_rule *rules, size_t rules_count, get_net getter
) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct net6_count n6_count = getter(rules + i);
		for (size_t j = 0; j < n6_count.count; ++j) {
			if (!validate_net6(n6_count.nets + j)) {
				return -1;
			}
		}
		cnt += n6_count.count;
	}
	return cnt;
}

static size_t
fill_segments(
	struct segment *segments,
	const struct filter_rule *rules,
	size_t rules_count,
	get_net getter,
	int part
) {
	size_t cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct net6_count cur = getter(rules + i);
		for (size_t j = 0; j < cur.count; ++j) {
			uint64_t from, to;
			net6_part_from_to(cur.nets + j, part, &from, &to);
			segments[cnt++] =
				(struct segment){.from = from, .to = to};
		}
	}

	qsort(segments, cnt, sizeof(struct segment), compare_segments);

	uint64_t max_right = 0;
	size_t taken = 0;
	for (size_t i = 0; i < cnt; ++i) {
		if (segments[i].to > max_right || taken == 0) {
			segments[taken++] = segments[i];
			max_right = segments[i].to;
		}
	}

	return taken;
}

static int
init_classifier_part(
	get_net getter,
	struct memory_context *mctx,
	size_t segments_count,
	struct net6_fast_classifier_part *classifier,
	int part /* 0/1 */,
	const struct filter_rule *rules,
	size_t rules_count
) {
	struct segment *segments =
		malloc(sizeof(struct segment) * segments_count);
	size_t after_collapse_cnt =
		fill_segments(segments, rules, rules_count, getter, part);

	uint64_t *from = malloc(sizeof(uint64_t) * after_collapse_cnt);
	for (size_t i = 0; i < after_collapse_cnt; ++i) {
		from[i] = segments[i].from;
	}

	classifier->to =
		memory_balloc(mctx, sizeof(uint64_t) * after_collapse_cnt);
	if (classifier->to == NULL && after_collapse_cnt > 0) {
		goto free_from;
	}
	for (size_t i = 0; i < after_collapse_cnt; ++i) {
		classifier->to[i] = segments[i].to;
	}
	SET_OFFSET_OF(&classifier->to, classifier->to);

	if (btree_u64_init(
		    &classifier->btree, from, after_collapse_cnt, mctx
	    ) != 0) {
		goto free_to;
	}

	free(segments);
	free(from);

	return 0;

free_to:
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->to),
		sizeof(uint64_t) * after_collapse_cnt
	);

free_from:
	free(from);
	free(segments);

	return -1;
}

static void
classifier_net_indices(
	struct net6 *net,
	struct net6_fast_classifier *classifier,
	size_t *idx_high,
	size_t *idx_low
) {
	uint64_t *to_array_high = ADDR_OF(&classifier->high.to);
	uint64_t *to_array_low = ADDR_OF(&classifier->low.to);

	uint64_t from_high, to_high;
	net6_part_from_to(net, 0, &from_high, &to_high);
	*idx_high =
		btree_u64_lower_bound(&classifier->high.btree, from_high + 1);
	assert(*idx_high > 0);
	--*idx_high;
	assert(to_array_high[*idx_high] >= to_high);

	uint64_t from_low, to_low;
	net6_part_from_to(net, 1, &from_low, &to_low);
	*idx_low = btree_u64_lower_bound(&classifier->low.btree, from_low + 1);
	assert(*idx_low > 0);
	--*idx_low;
	assert(to_array_low[*idx_low] >= to_low);
}

static int
comb_get(
	struct net6_fast_classifier *classifier, size_t idx_high, size_t idx_low
) {
	int res;
	size_t linear_idx = idx_high * classifier->low.btree.n + idx_low;
	memcpy(&res,
	       big_array_get(&classifier->comb, sizeof(int) * linear_idx),
	       sizeof(int));
	return res;
}

static void
comb_set(
	struct net6_fast_classifier *classifier,
	size_t idx_high,
	size_t idx_low,
	int value
) {
	size_t linear_idx = idx_high * classifier->low.btree.n + idx_low;
	memcpy(big_array_get(&classifier->comb, sizeof(int) * linear_idx),
	       &value,
	       sizeof(int));
}

static int
compare_ints(const void *left_void, const void *right_void) {
	return *(const int *)left_void - *(const int *)right_void;
}

static void
comb_compact(struct big_array *comb) {
	size_t n = comb->size / sizeof(int);
	int *values = malloc(n * sizeof(int));
	for (size_t i = 0; i < n; ++i) {
		memcpy(&values[i],
		       big_array_get(comb, sizeof(int) * i),
		       sizeof(int));
	}
	qsort(values, n, 4, compare_ints);
	for (size_t i = 0; i < n; ++i) {
		int value;
		memcpy(&value, big_array_get(comb, i * sizeof(int)), sizeof(int)
		);
		int left = 0;
		int right = n;
		while (left + 1 < right) {
			int mid = (left + right) / 2;
			if (values[mid] <= value) {
				left = mid;
			} else {
				right = mid;
			}
		}
		memcpy(big_array_get(comb, i * sizeof(int)), &left, sizeof(int)
		);
	}
	free(values);
}

static int
fill_classifier_comb(
	get_net getter,
	struct memory_context *mctx,
	struct net6_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count
) {
	size_t high_classifiers = classifier->high.btree.n;
	size_t low_classifiers = classifier->low.btree.n;
	if (big_array_init(
		    &classifier->comb,
		    sizeof(int) * high_classifiers * low_classifiers,
		    mctx
	    ) != 0) {
		return -1;
	}
	for (size_t i = 0; i < high_classifiers; ++i) {
		for (size_t j = 0; j < low_classifiers; ++j) {
			comb_set(classifier, i, j, -1);
		}
	}

	int counter = 0;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct net6_count cur = getter(rules + rule_idx);
		for (size_t net_idx = 0; net_idx < cur.count; ++net_idx) {
			size_t idx_high, idx_low;
			classifier_net_indices(
				cur.nets + net_idx,
				classifier,
				&idx_high,
				&idx_low
			);
			int idx = comb_get(classifier, idx_high, idx_low);
			if (idx == -1) {
				comb_set(
					classifier, idx_high, idx_low, counter++
				);
			}
		}
	}

	comb_compact(&classifier->comb);

	return 0;
}

static int
fill_classifier_registry(
	get_net getter,
	struct net6_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	struct value_registry *registry
) {
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct net6_count cur = getter(rules + rule_idx);
		if (value_registry_start(registry) != 0) {
			return -1;
		}
		for (size_t net_idx = 0; net_idx < cur.count; ++net_idx) {
			size_t idx_high, idx_low;
			classifier_net_indices(
				cur.nets + net_idx,
				classifier,
				&idx_high,
				&idx_low
			);

			int idx = comb_get(classifier, idx_high, idx_low);
			assert(idx != -1);

			if (value_registry_collect(registry, idx) != 0) {
				return -1;
			}
		}
	}

	return 0;
}

static void
setup_mismatch_classifier(
	struct net6_fast_classifier *classifier, struct value_registry *registry
) {
	classifier->mismatch_classifier = value_registry_capacity(registry);
	size_t high_classifiers = classifier->high.btree.n;
	size_t low_classifiers = classifier->low.btree.n;
	for (size_t i = 0; i < high_classifiers; ++i) {
		for (size_t j = 0; j < low_classifiers; ++j) {
			if (comb_get(classifier, i, j) == -1) {
				comb_set(
					classifier,
					i,
					j,
					classifier->mismatch_classifier
				);
			}
		}
	}
}

static int
init_classifier_comb_and_registry(
	get_net getter,
	struct memory_context *mctx,
	struct net6_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	struct value_registry *registry
) {
	if (fill_classifier_comb(
		    getter, mctx, classifier, rules, rules_count
	    ) != 0) {
		return -1;
	}

	if (fill_classifier_registry(
		    getter, classifier, rules, rules_count, registry
	    ) != 0) {
		big_array_free(&classifier->comb);
		return -1;
	}

	setup_mismatch_classifier(classifier, registry);

	return 0;
}

static void
free_classifier_part(
	struct net6_fast_classifier_part *classifier,
	struct memory_context *mctx
) {
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->to),
		classifier->btree.n * sizeof(uint64_t)
	);
	btree_u64_free(&classifier->btree);
}

static int
init_classifier(
	get_net getter,
	struct memory_context *mctx,
	struct net6_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	struct value_registry *registry
) {
	int segments_count = validate_and_count(rules, rules_count, getter);
	if (segments_count < 0) {
		return -2;
	}
	if (init_classifier_part(
		    getter,
		    mctx,
		    segments_count,
		    &classifier->high,
		    0,
		    rules,
		    rules_count
	    ) != 0) {
		return -1;
	}
	if (init_classifier_part(
		    getter,
		    mctx,
		    segments_count,
		    &classifier->low,
		    1,
		    rules,
		    rules_count
	    ) != 0) {
		free_classifier_part(&classifier->high, mctx);
		return -1;
	}
	if (init_classifier_comb_and_registry(
		    getter, mctx, classifier, rules, rules_count, registry
	    ) != 0) {
		free_classifier_part(&classifier->high, mctx);
		free_classifier_part(&classifier->low, mctx);
		return -1;
	}
	return 0;
}

static void
free_classifier(
	struct net6_fast_classifier *classifier, struct memory_context *mctx
) {
	free_classifier_part(&classifier->low, mctx);
	free_classifier_part(&classifier->high, mctx);
	big_array_free(&classifier->comb);
}

static struct net6_count
get_src(const struct filter_rule *rule) {
	struct net6_count res;
	res.count = rule->net6.src_count;
	res.nets = rule->net6.srcs;
	return res;
}

static struct net6_count
get_dst(const struct filter_rule *rule) {
	struct net6_count res;
	res.count = rule->net6.dst_count;
	res.nets = rule->net6.dsts;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_fast_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net6_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return init_classifier(
		get_dst,
		memory_context,
		classifier,
		rules,
		rules_count,
		registry
	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_fast_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net6_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return init_classifier(
		get_src,
		memory_context,
		classifier,
		rules,
		rules_count,
		registry
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier =
		(struct net6_fast_classifier *)data;
	free_classifier(classifier, memory_context);
	memory_bfree(
		memory_context, classifier, sizeof(struct net6_fast_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier =
		(struct net6_fast_classifier *)data;
	free_classifier(classifier, memory_context);
	memory_bfree(
		memory_context, classifier, sizeof(struct net6_fast_classifier)
	);
}
