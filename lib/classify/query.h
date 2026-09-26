/**
 * @file query.h
 * @brief Join and decode helpers for classification results.
 *
 * The helpers combine the per attribute classes of a packet batch into
 * the final classes and resolve them into rule indices. They are packet
 * agnostic: the caller reads the attribute classes with its own
 * lookups, and passes absolute pointers to the join tables and decoder
 * lines embedded in its classifier.
 */

#pragma once

#include <stdint.h>

#include "classify.h"

/*
 * Joins the classes of two classifier fields through one value table.
 *
 * A field pair resolves through the table the compiler built over the
 * ruleset, so the caller passes the joint of exactly the two sides the
 * classes came from.
 */
static inline void
classify_joint_lookup(
	const struct value_table *joint,
	const uint32_t *left_classes,
	const uint32_t *right_classes,
	uint32_t *classes,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		classes[idx] = value_table_get(
			joint, left_classes[idx], right_classes[idx]
		);
	}
}

/*
 * Joins the classes of two classifier fields and resolves the joint
 * classes into rule indices.
 *
 * The joint must be the root join of exactly the two sides the classes
 * came from and the line its decoder. A packet no rule of the
 * projection matches keeps the invalid rule mark.
 */
static inline void
classify_combine(
	const struct value_table *root_joint,
	const struct vline *rule_map,
	const uint32_t *left_classes,
	const uint32_t *right_classes,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint32_t cls = value_table_get(
			root_joint, left_classes[idx], right_classes[idx]
		);
		results[idx] = vline_get((struct vline *)rule_map, cls);
	}
}

/*
 * Resolves the final classes into rule indices through a decoder line.
 *
 * A packet no rule of the projection matches keeps the invalid rule
 * mark.
 */
static inline void
classify_resolve(
	const struct vline *rule_map,
	const uint32_t *classes,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] =
			vline_get((struct vline *)rule_map, classes[idx]);
	}
}
