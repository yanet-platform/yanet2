#include <errno.h>
#include <stdio.h>

#include "common/memory.h"
#include "controlplane/agent/agent.h"

#include "../dataplane/module.h"
#include "module.h"

#include <common/container_of.h>

#include <filter/compiler.h>
#include <filter/filter.h>

////////////////////////////////////////////////////////////////////////////////

#define ACL_FILTER_NET4_TAG __ACL_FILTER_NET4_TAG

FILTER_COMPILER_DECLARE(
	ACL_FILTER_NET4_TAG, proto_range, port_src, port_dst, net4_src, net4_dst
);

static int
filter_net4_compile(
	struct filter *filter,
	size_t rule_count,
	struct filter_rule *rules,
	struct memory_context *mctx
) {
	return FILTER_INIT(
		filter, ACL_FILTER_NET4_TAG, rules, rule_count, mctx
	);
}

////////////////////////////////////////////////////////////////////////////////

#define ACL_FILTER_NET6_TAG __ACL_FILTER_NET6_TAG

FILTER_COMPILER_DECLARE(
	ACL_FILTER_NET6_TAG, proto_range, port_src, port_dst, net6_src, net6_dst
);

static int
filter_net6_compile(
	struct filter *filter,
	size_t rule_count,
	struct filter_rule *rules,
	struct memory_context *mctx
) {
	return FILTER_INIT(
		filter, ACL_FILTER_NET6_TAG, rules, rule_count, mctx
	);
}

////////////////////////////////////////////////////////////////////////////////

static inline struct filter_rule *
acl_rules_into_filter_rules(acl_rule_t *rule) {
	return (struct filter_rule *)rule;
}

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
acl_module_config_create(
	struct agent *agent,
	const char *name,
	size_t rule_count,
	acl_rule_t *rules
) {
	struct acl_module_config *config =
		(struct acl_module_config *)memory_balloc(
			&agent->memory_context, sizeof(struct acl_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "acl", name)) {
		goto fail;
	}

	struct memory_context *mctx = &config->cp_module.memory_context;

	for (uint64_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		struct filter_rule *rule = rules + rule_idx;
		for (uint64_t device_idx = 0; device_idx < rule->device_count;
		     ++device_idx) {
			if (cp_module_link_device(
				    &config->cp_module,
				    rule->devices[device_idx].name,
				    &rule->devices[device_idx].id
			    )) {
				goto error_device;
			}
		}
	}

	if (filter_net4_compile(
		    &config->net4_filter,
		    rule_count,
		    acl_rules_into_filter_rules(rules),
		    mctx
	    ) != 0) {
		errno = ENOMEM;
		goto fail;
	}

	if (filter_net6_compile(
		    &config->net6_filter,
		    rule_count,
		    acl_rules_into_filter_rules(rules),
		    mctx
	    ) != 0) {
		errno = ENOMEM;
		goto free_filter4;
	}

	return &config->cp_module;

free_filter4: {
	int prev_errno = errno;
	FILTER_FREE(&config->net4_filter, ACL_FILTER_NET4_TAG);
	acl_module_config_free(&config->cp_module);
	errno = prev_errno;
}

error_device:

fail: {
	int prev_errno = errno;
	acl_module_config_free(&config->cp_module);
	errno = prev_errno;
	return NULL;
}
}

void
acl_module_config_free(struct cp_module *cp_module) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	memory_bfree(
		&agent->memory_context, config, sizeof(struct acl_module_config)
	);
}
