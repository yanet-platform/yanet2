/**
 * @file classify.h
 * @brief Core types of the compositional classification library.
 *
 * A classifier is an explicitly described tree - every attribute
 * builder returns a leaf classifier and the join of two classifiers
 * returns their parent - so a ruleset classification is authored as
 * joint(joint(attr, attr), attr) in code. Compilation runs in two
 * stages: classify_leaf and classify_join (or the classify_build
 * convenience over a flat signature) build the classifier tree with
 * its class space, and classify_decode builds the decoder mapping the
 * final class to the first rule index of a projection. A filter is a
 * frozen classifier tape plus its decoder.
 *
 * Usage overview:
 *  1) Declare attribute signatures with CLASSIFY_DECLARE /
 *     CLASSIFY_QUERY_DECLARE
 *  2) Build rules (array of struct filter_rule)
 *  3) Build the classifier tree with classify_leaf / classify_join
 *     over the union ruleset, or classify_build over a flat signature
 *  4) classify_decode for every projection of the ruleset
 *  5) classify_filter_init to freeze a classifier and decoder into a
 *     filter, classify_query to classify packets
 *
 * Ownership: a joined classifier borrows the tapes of its children, so
 * children are freed after their parents; a filter borrows the leaf
 * classifiers and join tables of its tree, so filters are freed before
 * the classifiers they were built from.
 *
 * Thread-safety:
 *  - Query is read-only and can be called concurrently for the same
 *    filter
 *  - Building/freeing must be exclusive
 */
#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include <stdint.h>

#define CLASSIFY_RULE_INVALID (uint32_t)0xffffffff
#define FILTER_RULE_INVALID CLASSIFY_RULE_INVALID

/*
 * Group of rules sharing the exact same attribute value inside one
 * attribute; identifies no group.
 */
#define FILTER_GROUP_INVALID (uint32_t)0xffffffff

struct classify_query_attr {};

/*
 * Frozen classification tape of a filter: the attribute lookups in
 * evaluation order, the join tables with their input slots, and the
 * decoder from the final class to the first rule index.
 */
struct classify_filter {
	struct classify_query_attr **attrs;
	struct value_table *joints;
	// Input value slots of every joint, two indexes per joint: the
	// value array positions the joint combines.
	uint32_t *joint_sides;
	uint32_t attr_count;
	uint32_t joint_count;
	struct vline *rule_map;
	struct memory_context memory_context;
};

static inline uint64_t
classify_filter_memory_usage(struct classify_filter *filter) {
	struct memory_context *mctx = &filter->memory_context;
	assert(mctx->balloc_size >= mctx->bfree_size);
	return mctx->balloc_size - mctx->bfree_size;
}
