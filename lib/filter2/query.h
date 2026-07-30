/**
 * @file query.h
 * @brief Query helpers and macro interface for classifying packets.
 *
 * Provides:
 *  - filter_actions_with_category(): post-process action list by category
 *  - FILTER_QUERY: run classification for a declared attribute signature
 *
 * Notes:
 *  - FILTER_QUERY returns a pointer to an actions array stored inside filter
 *    memory; it must not be freed by the caller.
 *  - Action iteration preserves order and stops at the first terminal action
 *    (i.e. without ACTION_NON_TERMINATE).
 */
#pragma once

#include <stdint.h>

#include "filter.h"
#include "query/attribute.h"
#include "rule.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
filter_process_joint(
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
filter_lookup(
	struct filter *filter,
	const struct filter_query_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	if (packet_count == 0)
		return;
	uint32_t joint_count = attr_handler_count - 1;
	uint32_t values[packet_count * (attr_handler_count + joint_count)];
	uint32_t values_pos = 0;

	struct filter_query_attr **attrs = ADDR_OF(&filter->attrs);
	/*
	 * Retrieve attribute values for each rule
	 */
	for (uint32_t attr_idx = 0; attr_idx < attr_handler_count; ++attr_idx) {
		const struct filter_query_attr *attr =
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
	 * Combine pair of values to get a next one value. The last of the
	 * values for each packet is a rule index.
	 */
	struct value_table *joints = ADDR_OF(&filter->joints);
	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		filter_process_joint(
			joints + joint_idx,
			values + joint_idx * 2 * packet_count,
			values + (joint_idx * 2 + 1) * packet_count,
			values + values_pos,
			packet_count
		);
		values_pos += packet_count;
	}

	memcpy(results,
	       values + values_pos - packet_count,
	       sizeof(uint32_t) * packet_count);
}

#define filter_query(filter, sign, packets, results, count)                    \
	filter_lookup(                                                         \
		filter,                                                        \
		sign,                                                          \
		sizeof(sign) / sizeof(*sign),                                  \
		(const struct packet **)packets,                               \
		results,                                                       \
		count                                                          \
	)
