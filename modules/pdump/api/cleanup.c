#include "cleanup.h"

#include <stdlib.h>
#include <string.h>

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/reclaim.h"
#include "lib/controlplane/config/zone.h"

static bool
pdump_cleanup_owned_by_predecessor(struct agent *agent, struct agent *owner) {
	for (struct agent *previous = ADDR_OF(&agent->prev); previous != NULL;
	     previous = ADDR_OF(&previous->prev)) {
		if (previous == owner) {
			return true;
		}
	}
	return false;
}

void
pdump_cleanup_targets_free(struct pdump_cleanup_targets *targets) {
	if (targets != NULL) {
		free(targets->modules);
		free(targets->rings);
		free(targets);
	}
}

struct pdump_cleanup_targets *
pdump_cleanup_targets_new(struct agent *agent, yanet_error **err) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct pdump_cleanup_targets *targets = calloc(1, sizeof(*targets));
	if (targets == NULL) {
		yanet_error_add(
			err, "failed to allocate pdump cleanup targets"
		);
		return NULL;
	}

	cp_config_lock(cp_config);
	struct cp_config_gen *generation = cp_config_gen_acquire(cp_config);
	size_t module_slots =
		cp_module_registry_size(&generation->module_registry);
	size_t object_slots =
		cp_object_registry_capacity(&generation->object_registry);
	targets->modules = calloc(module_slots, sizeof(*targets->modules));
	targets->rings = calloc(object_slots, sizeof(*targets->rings));
	if ((module_slots != 0 && targets->modules == NULL) ||
	    (object_slots != 0 && targets->rings == NULL)) {
		yanet_error_add(err, "failed to allocate pdump cleanup names");
		goto error;
	}

	spinlock_lock(&cp_config->agent_lock);
	if (!agent_is_current(cp_config, agent)) {
		spinlock_unlock(&cp_config->agent_lock);
		yanet_error_add_kind(
			err,
			YANET_ERROR_FAILED_PRECONDITION,
			"pdump cleanup requires the current agent"
		);
		goto error;
	}
	for (size_t idx = 0; idx < module_slots; ++idx) {
		struct cp_module *module = cp_module_registry_get(
			&generation->module_registry, idx
		);
		if (module != NULL && !strcmp(module->type, "pdump") &&
		    pdump_cleanup_owned_by_predecessor(
			    agent, ADDR_OF(&module->entity.agent)
		    )) {
			memcpy(targets->modules[targets->module_count++],
			       module->name,
			       sizeof(module->name));
		}
	}
	for (size_t idx = 0; idx < object_slots; ++idx) {
		struct cp_object *object = cp_object_registry_get(
			&generation->object_registry, idx
		);
		if (object != NULL && !strcmp(object->type, "ring") &&
		    pdump_cleanup_owned_by_predecessor(
			    agent, ADDR_OF(&object->entity.agent)
		    )) {
			memcpy(targets->rings[targets->ring_count++],
			       object->name,
			       sizeof(object->name));
		}
	}
	spinlock_unlock(&cp_config->agent_lock);
	cp_config_unlock(cp_config);
	cp_config_gen_release(cp_config, generation);
	return targets;

error:
	cp_config_unlock(cp_config);
	cp_config_gen_release(cp_config, generation);
	pdump_cleanup_targets_free(targets);
	return NULL;
}
