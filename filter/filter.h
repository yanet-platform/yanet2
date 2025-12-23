/**
 * @file filter.h
 * @brief Core types and utilities for the header-only filter (classifier) API.
 *
 * The filter is a static classification tree built over an explicit, ordered
 * set of attributes (the “signature”). It is constructed with FILTER_INIT()
 * and queried with FILTER_QUERY() using helpers from compiler.h and query.h.
 *
 * Key concepts:
 * - struct filter:     owns the tree (vertices, registries, tables) and memory
 * - struct filter_vertex: a node (leaf or inner) of the classification tree
 *
 * Usage overview:
 *  1) Declare attribute signature with FILTER_COMPILER_DECLARE /
 * FILTER_QUERY_DECLARE 2) Build rules (array of struct filter_rule) 3)
 * FILTER_INIT(...) to build tree into struct filter 4) FILTER_QUERY(...) to
 * classify a packet and get actions 5) FILTER_FREE(...) to release resources
 *
 * Thread-safety:
 *  - Query is read-only and can be called concurrently for the same filter
 *  - Building/freeing must be exclusive
 *
 * Limits:
 *  - MAX_ATTRIBUTES sets the upper bound on attributes per signature
 */
#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include <stdint.h>
#include <threads.h>

/**
 * @def MAX_ATTRIBUTES
 * @brief Upper bound on attribute count in a filter signature.
 * Increase with care; affects vertex storage and slot sizing.
 */
#define MAX_ATTRIBUTES 10

/**
 * @brief A node of the classification tree (leaf or inner).
 *
 * Leaf:
 *  - registry: holds per-attribute value ranges
 *  - data:     attribute-specific payload used by query helper
 * Inner:
 *  - table:    value_table merged from children
 *  - registry: merged registry for next level
 */
struct filter_vertex {
	struct value_registry registry;
	struct value_table table;
	void *data; // relative pointer compatible
};

/**
 * @brief Filter instance built for a fixed attribute signature.
 *
 * Layout:
 *  - v: array-based binary tree (1..n-1 inner, n..2n-1 leaves, 0 root when n=1)
 *  - memory_context: owns all registries/tables backing the filter
 *
 * Notes:
 *  - Query is read-only and can be called concurrently.
 *  - Memory of returned actions belongs to this filter.
 */
struct filter {
	struct filter_vertex v[2 * MAX_ATTRIBUTES];
	struct memory_context memory_context;
};