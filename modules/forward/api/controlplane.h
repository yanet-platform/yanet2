#pragma once

#include <stdint.h>

#include "lib/counters/counters.h"

#include "lib/controlplane/config/defines.h"

#include "lib/errors/errors.h"

#define FORWARD_MODE_NONE 0
#define FORWARD_MODE_IN 1
#define FORWARD_MODE_OUT 2

struct agent;
struct cp_module;

/*
 * The decision one rule of the linked classifier object's ruleset
 * resolves to: the forwarding target, its direction and its counter.
 *
 * The decisions are indexed by the classification results the object
 * yields, so the module build validates their count against the
 * object's ruleset.
 */
struct forward_decision {
	char target[CP_DEVICE_NAME_LEN];
	char counter[COUNTER_NAME_LEN];

	uint8_t mode;
};

/*
 * Allocate the forward module config and bind it to a classifier
 * object in one step: a config handle is fully built at construction
 * and never updated afterwards.
 *
 * The classifier object 'classifier:<classifier_object_name>' must be
 * installed (agent_update_objects) before this runs: the module links
 * it by name, resolves the object's device names into its own device
 * table so the executing device identifies itself, and validates
 * decision_count against the object's ruleset.
 *
 * Returns NULL with err set on failure; nothing is left allocated.
 */
struct cp_module *
forward_module_config_init(
	struct agent *agent,
	const char *name,
	const char *classifier_object_name,
	const struct forward_decision *decisions,
	uint32_t decision_count,
	yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
forward_module_config_free(struct cp_module *cp_module, yanet_error **err);
