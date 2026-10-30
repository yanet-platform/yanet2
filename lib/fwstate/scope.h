#pragma once

#include "types.h"

#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/classifiers/net6_hash.h"
#include "lib/classify/classify.h"
#include "lib/classify/query.h"
#include "lib/classify/rule_table.h"
#include "modules/fwstate/dataplane/config.h"

#include <stdbool.h>
#include <stdint.h>

/*
 * Reports whether a sync frame is inside the synchronization scope.
 *
 * The frame's source address classifies through its family's hashed
 * network attribute and its source port through the port attribute, and
 * the pair resolves through the family's rule table: a frame matches
 * when a scope rule names both. The zeroed scope - no rules - matches
 * everything, so the scope check is a no-op for unscoped configs.
 */
static inline bool
fwstate_scope_matches(
	const struct fwstate_scope *scope,
	const struct fw_state_sync_frame *frame
) {
	if (scope->net4_rules.rows == NULL && scope->net6_rules.rows == NULL) {
		return true;
	}

	uint32_t port_class = vline_get(
		(struct vline *)&scope->port_attr.line, frame->src_port
	);

	uint32_t net_class;
	uint32_t rule = FILTER_RULE_INVALID;
	if (frame->addr_type == FW_STATE_ADDR_TYPE_IP6) {
		net_class = classify_net6_hash_lookup1(
			&scope->net6_attr, frame->src_ip6
		);
		classify_rule_table_lookup(
			&scope->net6_rules, &net_class, &port_class, &rule, 1
		);
	} else {
		net_class = classify_net4_hash_lookup1(
			&scope->net4_attr, frame->src_ip
		);
		classify_rule_table_lookup(
			&scope->net4_rules, &net_class, &port_class, &rule, 1
		);
	}

	return rule != FILTER_RULE_INVALID;
}
