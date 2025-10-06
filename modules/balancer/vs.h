#pragma once

#include "config.h"
#include <assert.h>
#include <filter/filter.h>

////////////////////////////////////////////////////////////////////////////////

#define VS_ID_INVALID (uint32_t)-1

////////////////////////////////////////////////////////////////////////////////
// Utils for V4 service lookups
////////////////////////////////////////////////////////////////////////////////

#define __V4_LOOKUP_TAG __BALANCER_V4_LOOKUP_TAG

FILTER_DECLARE(__V4_LOOKUP_TAG, &attribute_net4_dst, &attribute_port_dst);

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
v4_vs_lookup_get(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&balancer_config->v4_service_lookup,
		__V4_LOOKUP_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return VS_ID_INVALID;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

static inline int
v4_vs_lookup_init(
	struct balancer_module_config *balancer_config,
	struct memory_context *mctx,
	struct filter_rule *rules,
	size_t rule_count
) {
	return FILTER_INIT(
		&balancer_config->v4_service_lookup,
		__V4_LOOKUP_TAG,
		rules,
		rule_count,
		mctx
	);
}

static inline void
v4_vs_lookup_free(struct balancer_module_config *balancer_config) {
	FILTER_FREE(&balancer_config->v4_service_lookup, __V4_LOOKUP_TAG);
}

////////////////////////////////////////////////////////////////////////////////
// Utils for V6 service lookups
////////////////////////////////////////////////////////////////////////////////

#define __V6_LOOKUP_TAG __BALANCER_V6_LOOKUP_TAG

FILTER_DECLARE(__V6_LOOKUP_TAG, &attribute_net6_dst, &attribute_port_dst);

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
v6_vs_lookup_get(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&balancer_config->v6_service_lookup,
		__V6_LOOKUP_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return VS_ID_INVALID;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

static inline int
v6_vs_lookup_init(
	struct balancer_module_config *balancer_config,
	struct memory_context *mctx,
	struct filter_rule *rules,
	size_t rule_count
) {
	return FILTER_INIT(
		&balancer_config->v6_service_lookup,
		__V6_LOOKUP_TAG,
		rules,
		rule_count,
		mctx
	);
}

static inline void
v6_vs_lookup_free(struct balancer_module_config *balancer_config) {
	FILTER_FREE(&balancer_config->v6_service_lookup, __V6_LOOKUP_TAG);
}