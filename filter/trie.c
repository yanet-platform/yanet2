#include "trie.h"
#include "common/exp_array.h"
#include "common/memory.h"
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static void
init_vertex(struct trie_vertex *v) {
	v->rules = NULL;
	v->rule_count = 0;
	v->classifier = 0;
	memset(v->next, TRIE_VERTEX_UNDEF, 256 * sizeof(uint32_t));
}

static int
add_vertex(struct trie *trie) {
	void *data = trie->vertices;
	int ret = mem_array_expand_exp(
		trie->mctx,
		&data,
		sizeof(struct trie_vertex),
		&trie->vertex_count
	);
	if (ret < 0) {
		return -1;
	}
	trie->vertices = data;
	init_vertex(&trie->vertices[trie->vertex_count - 1]);
	return 0;
}

static inline int
transition(
	struct trie *trie, uint32_t vertex_id, uint8_t next, uint32_t *next_v
) {
	struct trie_vertex *v = &trie->vertices[vertex_id];
	if (v->next[next] == TRIE_VERTEX_UNDEF) {
		int ret = add_vertex(trie);
		if (ret < 0) {
			return -1;
		}
		v = &trie->vertices[vertex_id];
		v->next[next] = trie->vertex_count - 1;
	}
	*next_v = v->next[next];
	return 0;
}

static int
append_rule(struct trie *trie, uint32_t vertex_id, uint32_t rule) {
	struct trie_vertex *v = &trie->vertices[vertex_id];
	void *data = v->rules;
	int ret = mem_array_expand_exp(
		trie->mctx, &data, sizeof(uint32_t), &v->rule_count
	);
	if (ret < 0) {
		return ret;
	}
	v->rules = data;
	v->rules[v->rule_count - 1] = rule;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
trie_init(struct trie *trie, struct memory_context *mctx) {
	trie->vertex_count = 0;
	trie->vertices = NULL;
	trie->mctx = mctx;
	return add_vertex(trie);
}

////////////////////////////////////////////////////////////////////////////////

static int
add_rec(struct trie *trie,
	uint32_t vertex_id,
	const uint8_t *value,
	uint32_t prefix_len,
	uint32_t rule) {
	if (prefix_len == 0) {
		return append_rule(trie, vertex_id, rule);
	}
	int ret;
	if (prefix_len >= 8) {
		uint32_t next_vertex_id;
		ret = transition(trie, vertex_id, value[0], &next_vertex_id);
		if (ret < 0) {
			return -1;
		}
		return add_rec(
			trie, next_vertex_id, value + 1, prefix_len - 8, rule
		);
	}
	uint8_t mask = ~((1 << (8 - prefix_len)) - 1);
	uint8_t need = value[0] & mask;
	for (uint16_t next = 0; next <= 0xFF; ++next) {
		if ((next & mask) != need) {
			continue;
		}
		uint32_t next_vertex_id;
		ret = transition(trie, vertex_id, next, &next_vertex_id);
		if (ret < 0) {
			return -1;
		}
		ret = add_rec(trie, next_vertex_id, NULL, 0, rule);
		if (ret < 0) {
			return -1;
		}
	}
	return 0;
}

int
trie_add(
	struct trie *trie,
	const uint8_t *value,
	uint32_t prefix_len,
	uint32_t rule
) {
	return add_rec(trie, 0, value, prefix_len, rule);
}

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
next_classifier(
	struct trie *trie,
	uint32_t v_id,
	uint32_t par_id,
	uint8_t from,
	uint32_t *next_class
) {
	if (par_id == TRIE_VERTEX_UNDEF) {
		return (*next_class)++;
	}
	struct trie_vertex *v = &trie->vertices[v_id];
	struct trie_vertex *par = &trie->vertices[par_id];
	if (v->rule_count == 0) {
		return par->classifier;
	}
	for (uint8_t simb = 0; simb < from; ++simb) {
		uint32_t s_id = par->next[simb];
		if (s_id != TRIE_VERTEX_UNDEF) {
			struct trie_vertex *s = &trie->vertices[s_id];
			if (s->rule_count == v->rule_count &&
			    memcmp(s->rules,
				   v->rules,
				   s->rule_count * sizeof(uint32_t)) == 0) {
				return s->classifier;
			}
		}
	}
	return (*next_class)++;
}

static void
build_classifiers_rec(
	struct trie *trie,
	uint32_t v_id,
	uint32_t par_id,
	uint8_t from,
	uint32_t *next_class
) {
	struct trie_vertex *v = &trie->vertices[v_id];
	v->classifier = next_classifier(trie, v_id, par_id, from, next_class);
	for (uint16_t next = 0; next <= 0xFF; ++next) {
		uint32_t next_id = v->next[next];
		if (next_id != TRIE_VERTEX_UNDEF) {
			build_classifiers_rec(
				trie, next_id, v_id, next, next_class
			);
		}
	}
}

void
trie_build_classifiers(struct trie *trie) {
	uint32_t class = 0;
	build_classifiers_rec(trie, 0, TRIE_VERTEX_UNDEF, 0, &class);
}

////////////////////////////////////////////////////////////////////////////////

static int
collect_rule_classifiers_rec(
	const struct trie *trie,
	uint32_t v_id,
	struct rule_classifiers *cls,
	uint32_t *stack,
	uint32_t stack_size
) {
	stack[stack_size++] = v_id;
	const struct trie_vertex *v = &trie->vertices[v_id];
	if (v->rule_count > 0) {
		for (uint32_t i = 0; i < stack_size; ++i) {
			const struct trie_vertex *stack_v =
				&trie->vertices[stack[i]];
			for (uint32_t j = 0; j < stack_v->rule_count; ++j) {
				uint32_t rule = stack_v->rules[j];

				void *data = cls->classifiers[rule];
				int ret = mem_array_expand_exp(
					cls->mctx,
					&data,
					sizeof(uint32_t),
					&cls->count[rule]
				);
				if (ret < 0) {
					return -1;
				}
				cls->classifiers[rule] = data;
				cls->classifiers[rule][cls->count[rule] - 1] =
					v->classifier;
			}
		}
	}
	for (uint16_t next = 0; next <= 0xFF; ++next) {
		uint32_t next_id = v->next[next];
		if (next_id != TRIE_VERTEX_UNDEF) {
			int ret = collect_rule_classifiers_rec(
				trie, next_id, cls, stack, stack_size
			);
			if (ret < 0) {
				return ret;
			}
		}
	}
	return 0;
}

#define MAX_TRIE_DEPTH 32

int
trie_collect_rule_classifiers(
	const struct trie *trie, struct rule_classifiers *cls
) {
	uint32_t stack[MAX_TRIE_DEPTH];
	return collect_rule_classifiers_rec(trie, 0, cls, stack, 0);
}

////////////////////////////////////////////////////////////////////////////////

void
trie_free_mem(struct trie *trie) {
	for (size_t i = 0; i < trie->vertex_count; ++i) {
		memory_bfree(
			trie->mctx,
			trie->vertices[i].rules,
			trie->vertices[i].rule_count * sizeof(uint32_t)
		);
	}
	memory_bfree(
		trie->mctx,
		trie->vertices,
		trie->vertex_count * sizeof(struct trie_vertex)
	);
}