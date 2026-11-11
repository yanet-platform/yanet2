#pragma once

#include "modules/l3b/api/controlplane.h"
#include "objects/l3b/api/l3b_session_table_object.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

// Construction witnesses distinguish IPv4 decode success from IPv6 entry.
//
// They also count allocation failures injected into IPv6 decoding.
struct l3b_probe_witness {
	int ip4_decode_succeeded;
	int ip6_decode_entered;
	int injected_allocations;
};

// Test calls borrow descriptors and tables; successful handles need release.
struct cp_object *
l3b_probe_service_create(
	const struct l3b_virtual_service_create_config *config,
	int fail_ip6_decode,
	struct l3b_probe_witness *witness,
	yanet_error **err
);

int
l3b_probe_service_free(struct cp_object *object, yanet_error **err);

struct cp_module *
l3b_probe_module_new(struct agent *agent, const char *name, yanet_error **err);

// Updates require a fresh unpublished module and a genuinely published service.
int
l3b_probe_module_update(
	struct cp_module *module,
	const struct l3b_destination_filter_rule *rules,
	uint32_t rule_count,
	int fail_ip6_decode,
	struct l3b_probe_witness *witness,
	yanet_error **err
);

int
l3b_probe_module_free(struct cp_module *module, yanet_error **err);

// A small real session table remains caller-owned across service failures.
struct cp_object *
l3b_probe_table_create(
	struct agent *agent, const char *name, yanet_error **err
);

int
l3b_probe_table_free(struct cp_object *object, yanet_error **err);

// The sentinel is a live flow with a fixed backend and deadline.
int
l3b_probe_table_seed(struct cp_object *object);

int
l3b_probe_table_has_sentinel(struct cp_object *object);
