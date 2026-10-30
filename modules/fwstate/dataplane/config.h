#pragma once

#define FWSTATE_MODULE_NAME "fwstate"

#include "common/filter_views.h"
#include "common/network.h"

#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/classifiers/net6_hash.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/rule_table.h"
#include "lib/controlplane/config/zone.h"

#include "lib/fwstate/config.h"
#include "objects/fwstate/api/fwstate_map_object.h"

// Sentinel for "no object link at this slot". object_link_get_address
// returns NULL for any index >= object_link_count, so a config with no
// map link at this slot resolves to a NULL fwtable.
#define FWSTATE_OBJECT_LINK_NONE UINT64_MAX

// How far one config has read the stashes of one worker in one round.
//
// The first entry follows the v4 map and the second the v6 map: the
// records below each were handled in the named round, and a position of
// an earlier round starts from zero. Every context of the config on the
// worker shares it, so each record is emitted once per config.
struct fwstate_stash_position {
	uint64_t iteration;
	uint32_t processed[2];
} __attribute__((__aligned__(64)));

// Counter addresses of one address family's sync frames.
struct fwstate_family_counters {
	uint64_t *inserted;
	uint64_t *insert_failed;
	uint64_t *suppressed;
};

// Counter addresses of the sync paths in one execution context's storage.
//
// The family entries follow the v4 and v6 maps.
struct fwstate_counters {
	uint64_t *sync_packets;
	uint64_t *passthrough;
	struct fwstate_family_counters family[2];
	uint64_t *external_dropped;
	uint64_t *internal_forwarded;
	uint64_t *sync_alloc_failed;
	uint64_t *scope_dropped;
};

// Per-worker links of one execution context, set by the module's commit
// handler for the worker that runs the context.
//
// The map links follow the v4 and v6 maps and the counters point into the
// context's own storage; the position is the config's own entry for that
// worker and is never reset by the commit handler.
struct fwstate_prepared {
	struct fwstate_map_link map[2];
	struct fwstate_counters counters;
	struct fwstate_stash_position *position;
};

/*
 * One synchronization scope rule: the flows a rule names are the ones
 * the module syncs. The networks match the flow's source address and
 * the port ranges its source port; a rule naming neither matches every
 * flow. An empty rule list syncs everything.
 *
 * The rule lives in the module config's memory tree: the views point
 * into the net and port arrays the control plane allocates alongside
 * the rule itself.
 */
struct fwstate_sync_rule {
	struct classifier_rule rule;
	struct filter_net4s net4s;
	struct filter_net6s net6s;
	// The port spans in the u16 attribute's own type: the ranges view
	// of the compile and the port_ranges view of the filter share the
	// layout.
	struct filter_u16_ranges src_ports;
};

// The compiled scope: the hashed network attributes of both families
// joined with the port attribute through first-match rule tables. The
// zeroed struct - no rules - matches everything.
struct fwstate_scope {
	struct classify_attr_net4_hash net4_attr;
	struct classifier net4_cls;
	struct classify_attr_net6_hash net6_attr;
	struct classifier net6_cls;
	struct classify_attr_port port_attr;
	struct classifier port_cls;
	struct rule_table net4_rules;
	struct rule_table net6_rules;
};

struct fwstate_module_config {
	struct cp_module cp_module;

	// Object link indices for the v4 and v6 fwtables, declared by the
	// module constructor and resolved at execution-context build time
	// into per-worker entries naming the linked fwstate-map objects.
	// The FWSTATE_OBJECT_LINK_NONE sentinel marks an absent link, and
	// that family's sync frames are then counted and dropped without
	// inserting.
	uint64_t v4_object_link_idx;
	uint64_t v6_object_link_idx;

	// Synchronization parameters for receive matching, state updates,
	// suppression, and local emission.
	struct fwstate_sync_config sync_config;

	// The synchronization scope rules and their compiled classifier;
	// an absent rule list leaves the scope matching everything.
	struct fwstate_sync_rule *rules;
	uint32_t rule_count;
	struct fwstate_scope scope;

	// Stash read positions, one per dataplane worker, allocated zeroed
	// with the config and freed with it.
	uint64_t worker_count;
	struct fwstate_stash_position *positions;

	// Module-level counters, registered by fwstate_module_config_new.
	// Each counter_id is resolved per-worker via counter_get_address().
	// size=2 counters hold [packets, bytes]; size=1 counters hold
	// [packets].
	uint64_t sync_packets_counter_id;
	uint64_t passthrough_counter_id;
	uint64_t sync_v4_inserted_counter_id;
	uint64_t sync_v6_inserted_counter_id;
	uint64_t sync_v4_insert_failed_counter_id;
	uint64_t sync_v6_insert_failed_counter_id;
	uint64_t sync_v4_suppressed_counter_id;
	uint64_t sync_v6_suppressed_counter_id;
	uint64_t external_dropped_counter_id;
	uint64_t internal_forwarded_counter_id;
	uint64_t sync_alloc_failed_counter_id;
	uint64_t scope_dropped_counter_id;
};
