#pragma once

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

#define TRIE_VERTEX_UNDEF ((uint32_t)-1)

////////////////////////////////////////////////////////////////////////////////

struct trie_vertex {
	uint32_t next[256];
	uint32_t *rules;
	uint64_t rule_count;
	uint32_t classifier;
};

////////////////////////////////////////////////////////////////////////////////

struct trie {
	struct memory_context *mctx;
	struct trie_vertex *vertices;
	uint64_t vertex_count;
};

////////////////////////////////////////////////////////////////////////////////

int
trie_init(struct trie *trie, struct memory_context *mctx);

int
trie_add(
	struct trie *trie,
	const uint8_t *value,
	uint32_t prefix_len,
	uint32_t rule
);

void
trie_build_classifiers(struct trie *trie);

uint32_t
trie_classify(const struct trie *trie, const uint8_t *value, uint32_t n);

////////////////////////////////////////////////////////////////////////////////

struct rule_classifiers {
	uint32_t **classifiers; // rule -> [classifier]
	uint64_t *count;	// rule -> len([classifier])
	struct memory_context *mctx;
};

int
trie_collect_rule_classifiers(
	const struct trie *trie, struct rule_classifiers *cls
);

////////////////////////////////////////////////////////////////////////////////

void
trie_free_mem(struct trie *trie);