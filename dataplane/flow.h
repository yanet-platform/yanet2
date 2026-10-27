#pragma once

#include <stdint.h>

#include "config.h"

// Steer the classes the queue roles list to their queues on a started port.
//
// Each class goes to every queue listing it: through a QUEUE action when
// one queue does, through an RSS action over them when several do. That
// action hashes with the port's RSS types, or with IP addresses when the
// port hashes nothing. Classes are matched untagged and VLAN-tagged;
// l2_control matches by destination MAC only.
//
// Any rule the driver rejects fails the whole call and removes the rules
// already installed, so a port never runs with a partial set. A driver with
// no flow API at all, such as virtio, fails on the first rule.
int
dataplane_flow_steer(
	uint16_t port_id,
	uint64_t rss_hash,
	const struct dataplane_queue_role *roles,
	uint16_t queue_count
);
