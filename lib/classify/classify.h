/**
 * @file classify.h
 * @brief Shared constants of the classification library.
 *
 * The library models a classification as explicitly composed attribute
 * classifiers joined through named value tables and decoded through
 * named value lines. A consumer defines its own classifier struct with
 * one named field per attribute it classifies by, compiles each field
 * with the matching attribute entry of the compiler headers, and joins
 * the field classes with the query helpers. The library owned
 * structures are exactly the two below - the rule base the ruleset is
 * addressed through and the compiled stage - everything else a
 * classification consists of lives in a consumer defined field, so its
 * ownership is documented by each consumer.
 */

#pragma once

#include "common/memory.h"
#include "common/registry.h"

#include <stdint.h>
#include <string.h>

/*
 * The classifier handle to a module rule: an empty base every module
 * rule embeds as its first member. The library addresses a ruleset
 * through pointers to the base alone, and a module getter recovers the
 * enclosing rule with container_of - no intermediate adaptation
 * between the two lives.
 */
struct classifier_rule {};

#define CLASSIFY_RULE_INVALID (uint32_t)0xffffffff
#ifndef FILTER_RULE_INVALID
#define FILTER_RULE_INVALID CLASSIFY_RULE_INVALID
#endif

/*
 * Group of rules sharing the exact same attribute value inside one
 * attribute; identifies no group.
 */
#define FILTER_GROUP_INVALID (uint32_t)0xffffffff

/*
 * A compiled classification stage over a ruleset projection: the
 * registry of its class space together with the rule group mapping of
 * the projection. Every attribute compile produces one, every join of
 * two stages produces the next, and the decoder resolves the final one
 * into rule indices.
 *
 * The compile and the join entries initialize the stage themselves, so
 * a caller hands them a zeroed or a stale-free struct; a successful
 * entry leaves the stage owned by the caller, released through the
 * fini below, and a failed one leaves it zeroed. The initialization
 * itself is library internal.
 */
struct classifier {
	struct value_registry registry;
	uint32_t *rule_groups;
};

// Releases a stage; safe on a zeroed one, and idempotent.
static inline void
classifier_fini(
	struct classifier *cls,
	struct memory_context *memory_context,
	uint32_t rule_count
) {
	value_registry_fini(&cls->registry);

	if (cls->rule_groups != NULL) {
		uint32_t rule_alloc = rule_count ? rule_count : 1;
		memory_bfree(
			memory_context,
			cls->rule_groups,
			sizeof(uint32_t) * rule_alloc
		);
		cls->rule_groups = NULL;
	}
}
