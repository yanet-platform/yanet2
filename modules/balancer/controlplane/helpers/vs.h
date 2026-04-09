#pragma once

#include <stddef.h>

struct balancer_vs;
struct agent;
struct rcu;

int
balancer_vs_set_acl(struct balancer_vs *vs, struct agent *agent);

void
balancer_vs_free_acl(struct balancer_vs *vs, struct agent *agent);

int
balancer_vs_update_real_selector(
	struct balancer_vs *vs, struct rcu *rcu, struct agent *agent
);

void
balancer_vs_free_real_selector(struct balancer_vs *vs, struct agent *agent);

int
balancer_vs_set_session_trackers(struct balancer_vs *vs, struct agent *agent);

void
balancer_vs_free_session_trackers(struct balancer_vs *vs, struct agent *agent);