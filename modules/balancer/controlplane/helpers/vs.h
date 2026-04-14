/*
 * Per-VS helpers: ACL compilation, real selector (ring) management,
 * and per-real session tracker allocation.
 *
 * Error convention: functions returning int use 0 for success and
 * non-zero for failure.
 *   -1  shared-memory allocation failure
 *   -2  heap allocation failure
 */
#pragma once

#include <stddef.h>

struct balancer_vs;
struct agent;
struct rcu;

/* Compile an ACL filter from the VS's allowed_sources list.
 * Sets vs->acl to NULL when allowed_sources_count == 0.
 * Returns 0 on success, -1 on shared memory allocation failure,
 * -2 on heap allocation failure. */
int
balancer_vs_set_acl(struct balancer_vs *vs, struct agent *agent);

void
balancer_vs_free_acl(struct balancer_vs *vs, struct agent *agent);

/* Rebuild the weighted ring selector from current effective_weight values.
 * Uses RCU to swap the active ring without disrupting dataplane lookups.
 * Returns 0 on success, -1 on failure. */
int
balancer_vs_update_real_selector(
	struct balancer_vs *vs, struct rcu *rcu, struct agent *agent
);

void
balancer_vs_free_real_selector(struct balancer_vs *vs, struct agent *agent);

/* Allocate per-worker session tracker shards for each real that doesn't
 * already have them (tracker_shards != NULL).
 * Returns 0 on success, -1 on allocation failure. */
int
balancer_vs_set_session_trackers(struct balancer_vs *vs, struct agent *agent);

void
balancer_vs_free_session_trackers(struct balancer_vs *vs, struct agent *agent);