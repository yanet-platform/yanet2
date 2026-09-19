/**
 * @file query.h
 * @brief Query helpers and macro interface for classifying packets.
 *
 * Provides:
 *  - classify_lookup(): run the frozen classifier tape of a filter
 *  - CLASSIFY_QUERY: run classification for a declared attribute
 *    signature
 *
 * Notes:
 *  - CLASSIFY_QUERY writes one rule index per packet into the caller
 *    supplied result array; CLASSIFY_RULE_INVALID marks a packet no
 *    rule of the filter projection matches.
 */
#pragma once

#include <stdint.h>

#include "classify.h"
#include "query/attribute.h"
#include "rule.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
classify_process_joint(
	struct value_table *value_table,
	uint32_t *v_values,
	uint32_t *h_values,
	uint32_t *o_values,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		o_values[idx] = value_table_get(
			value_table, v_values[idx], h_values[idx]
		);
	}
}

static inline void
classify_lookup(
	struct classify_filter *filter,
	const struct classify_query_attr_handlers *attr_handlers[],
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	if (packet_count == 0) {
		return;
	}
	uint32_t slot_count = filter->attr_count + filter->joint_count;
	uint32_t values[packet_count * slot_count];
	uint32_t values_pos = 0;

	struct classify_query_attr **attrs = ADDR_OF(&filter->attrs);
	/*
	 * Retrieve the attribute class values for every packet.
	 */
	for (uint32_t attr_idx = 0; attr_idx < filter->attr_count; ++attr_idx) {
		const struct classify_query_attr *attr =
			ADDR_OF(attrs + attr_idx);
		attr_handlers[attr_idx]->lookup(
			attr,
			attr_handlers[attr_idx],
			packets,
			values + values_pos,
			packet_count
		);

		values_pos += packet_count;
	}

	/*
	 * Combine the joint input slots into the joint outputs, the last
	 * value of the tape being the final class of the tree.
	 */
	struct value_table *joints = ADDR_OF(&filter->joints);
	const uint32_t *joint_sides = ADDR_OF(&filter->joint_sides);
	for (uint32_t joint_idx = 0; joint_idx < filter->joint_count;
	     ++joint_idx) {
		classify_process_joint(
			joints + joint_idx,
			values + joint_sides[joint_idx * 2] * packet_count,
			values + joint_sides[joint_idx * 2 + 1] * packet_count,
			values + values_pos,
			packet_count
		);
		values_pos += packet_count;
	}

	/*
	 * Translate the final class identifiers into rule indices.
	 */
	const uint32_t *class_ids = values + values_pos - packet_count;
	struct vline *rule_map = ADDR_OF(&filter->rule_map);
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		results[idx] = vline_get(rule_map, class_ids[idx]);
	}
}

#define classify_query(filter, sign, packets, results, count)                  \
	classify_lookup(                                                       \
		filter, sign, (const struct packet **)packets, results, count  \
	)
