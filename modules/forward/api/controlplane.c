#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/strutils.h"
#include "lib/errors/errors.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"

#include "objects/classifier/api/classifier_object.h"

static void
forward_module_config_destroy(struct cp_module *cp_module) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	memory_bfree(
		&cp_module->memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct forward_target) * config->target_count
	);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct forward_module_config)
	);
}

struct cp_module *
forward_module_config_init(
	struct agent *agent,
	const char *name,
	const char *classifier_object_name,
	const struct forward_decision *decisions,
	uint32_t decision_count,
	yanet_error **err
) {
	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "forward", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct forward_module_config)
		);
		return NULL;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;
	config->classifier_link_idx = FORWARD_CLASSIFIER_LINK_NONE;

	if (decision_count == 0) {
		yanet_error_add(
			err, "forward config must contain at least one rule"
		);
		goto error;
	}

	// The linked classifier object must already be installed: the
	// binding resolves its ruleset size and its device names at
	// construction, and the module update below only re-validates the
	// link's existence.
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_object *cp_object = cp_config_gen_lookup_object(
		config_gen, CLASSIFIER_OBJECT_TYPE, classifier_object_name
	);
	if (cp_object == NULL) {
		yanet_error_add(
			err,
			"classifier object '%s:%s' is not installed",
			CLASSIFIER_OBJECT_TYPE,
			classifier_object_name
		);
		goto error;
	}

	if (classifier_object_rule_count(cp_object) != decision_count) {
		yanet_error_add(
			err,
			"decision count %u does not match the %u rules of "
			"classifier object '%s:%s'",
			decision_count,
			classifier_object_rule_count(cp_object),
			CLASSIFIER_OBJECT_TYPE,
			classifier_object_name
		);
		goto error;
	}

	if (cp_module_link_object(
		    &config->cp_module,
		    CLASSIFIER_OBJECT_TYPE,
		    classifier_object_name,
		    &config->classifier_link_idx,
		    err
	    )) {
		goto error;
	}

	// The object's device names join the module's own device table,
	// so the link pass identifies the executing device among them and
	// the module's commit resolves that device's class in the
	// object's device attribute.
	for (uint64_t idx = 0; idx < classifier_object_device_count(cp_object);
	     ++idx) {
		uint64_t device_id;
		if (cp_module_link_device(
			    &config->cp_module,
			    classifier_object_device_name(cp_object, idx),
			    &device_id,
			    err
		    )) {
			goto error;
		}
	}

	struct forward_target *targets = (struct forward_target *)memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct forward_target) * decision_count
	);
	if (targets == NULL) {
		yanet_error_add(
			err, "failed to allocate the forwarding targets"
		);
		goto error;
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = decision_count;

	// Just collect and link all devices
	for (uint32_t idx = 0; idx < decision_count; ++idx) {
		if (cp_module_link_device(
			    &config->cp_module,
			    decisions[idx].target,
			    &targets[idx].device_id,
			    err
		    )) {
			goto error_target;
		}

		targets[idx].mode = decisions[idx].mode;

		if ((targets[idx].counter_id = counter_registry_register(
			     &config->cp_module.counter_registry,
			     decisions[idx].counter,
			     2,
			     err
		     )) == (uint64_t)-1) {
			goto error_target;
		}
	}

	return &config->cp_module;

error_target:
	memory_bfree(
		&config->cp_module.memory_context,
		targets,
		sizeof(struct forward_target) * decision_count
	);
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

error:
	forward_module_config_destroy(&config->cp_module);
	return NULL;
}

int
forward_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	forward_module_config_destroy(cp_module);
	return 0;
}
