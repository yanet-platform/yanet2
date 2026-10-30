#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/classifiers/net6_hash.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/net4_hash.h"
#include "lib/classify/compiler/net6_hash.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/rule_table.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/stash.h"
#include "lib/fwstate/sync.h"
#include "modules/fwstate/dataplane/config.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

// Set default timeout values for fwstate configuration
void
fwstate_config_set_defaults(struct fwstate_sync_config *config) {
	memset(config, 0, sizeof(struct fwstate_sync_config));
	config->timeouts.tcp_syn_ack = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_syn = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_fin = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.udp = 30e9;	  // 30 seconds
	config->timeouts.default_ = 16e9; // 16 seconds
	config->sync_mtu = FWSTATE_SYNC_DEFAULT_MTU;
}

static void
fwstate_module_config_destroy(struct cp_module *cp_module);

// Allocate one zeroed stash read position per dataplane worker.
static int
fwstate_module_positions_new(
	struct fwstate_module_config *config, struct agent *agent
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t worker_count = dp_config != NULL ? dp_config->worker_count : 0;
	if (worker_count == 0) {
		return 0;
	}
	struct fwstate_stash_position *positions = fwstate_stash_zalloc(
		&config->cp_module.memory_context,
		sizeof(struct fwstate_stash_position) * worker_count
	);
	if (positions == NULL) {
		return -1;
	}
	config->worker_count = worker_count;
	SET_OFFSET_OF(&config->positions, positions);
	return 0;
}

// Return the read positions to the module's memory context.
static void
fwstate_module_positions_free(struct fwstate_module_config *config) {
	struct fwstate_stash_position *positions = ADDR_OF(&config->positions);
	if (positions != NULL) {
		fwstate_stash_zfree(
			&config->cp_module.memory_context,
			positions,
			sizeof(struct fwstate_stash_position) *
				config->worker_count
		);
	}
	SET_OFFSET_OF(&config->positions, NULL);
	config->worker_count = 0;
}

// Declare the module's link to one family's fwstate-map object and
// record the link index.
//
// Construction only records the declaration: the name is validated when
// the generation carrying this module is installed, and the dataplane
// reads the fwtable through the link at packet time. A stale raw table
// pointer here would outlive the object across generations, which is why
// the config stores an index instead.
static int
fwstate_module_link_table(
	struct cp_module *cp_module,
	const char *map_name,
	bool is_ipv6,
	uint64_t *link_idx,
	yanet_error **err
) {
	const char *object_type = is_ipv6 ? FWSTATE_MAP_V6_OBJECT_TYPE
					  : FWSTATE_MAP_V4_OBJECT_TYPE;

	return cp_module_link_object(
		cp_module, object_type, map_name, link_idx, err
	);
}

// Field selectors of the scope rule: the module rule is handed to the
// classify compilers directly, every attribute through its getter.
static inline void
fwstate_rule_get_net4s(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct fwstate_sync_rule *sync_rule =
		container_of(rule, struct fwstate_sync_rule, rule);
	*nets = sync_rule->net4s;
}

static inline void
fwstate_rule_get_net6s(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct fwstate_sync_rule *sync_rule =
		container_of(rule, struct fwstate_sync_rule, rule);
	*nets = sync_rule->net6s;
}

static inline void
fwstate_rule_get_src_ports(
	const struct classifier_rule *rule, struct filter_u16_ranges *ports
) {
	const struct fwstate_sync_rule *sync_rule =
		container_of(rule, struct fwstate_sync_rule, rule);
	*ports = sync_rule->src_ports;
}

CLASSIFY_NET4_HASH_COMPILE(fwstate_net4, fwstate_rule_get_net4s)
CLASSIFY_NET6_HASH_COMPILE(fwstate_net6, fwstate_rule_get_net6s)
CLASSIFY_U16_RANGES_COMPILE(
	fwstate_port, struct classify_attr_port, fwstate_rule_get_src_ports
)

// Releases the compiled scope; safe on a zeroed one, so a destroy path
// is idempotent.
static void
fwstate_scope_free(
	struct memory_context *memory_context,
	struct fwstate_scope *scope,
	uint32_t rule_count
) {
	rule_table_free(&scope->net6_rules);
	classify_attr_net6_hash_free(memory_context, &scope->net6_attr);
	classifier_fini(&scope->net6_cls, memory_context, rule_count);

	rule_table_free(&scope->net4_rules);
	classify_attr_net4_hash_free(memory_context, &scope->net4_attr);
	classifier_fini(&scope->net4_cls, memory_context, rule_count);

	classify_attr_port_free(memory_context, &scope->port_attr);
	classifier_fini(&scope->port_cls, memory_context, rule_count);

	memset(scope, 0, sizeof(*scope));
}

// Copies the flat input rules into the config's memory tree and
// compiles them into the scope. A zero rule count leaves the scope
// zeroed - matching everything - without touching the classifiers.
//
// Returns -1 with the rules and the scope freed on failure.
static int
fwstate_scope_compile(
	struct memory_context *memory_context,
	struct fwstate_module_config *config,
	const struct fwstate_sync_rule_input *inputs,
	uint32_t rule_count
) {
	if (rule_count == 0) {
		return 0;
	}

	struct fwstate_sync_rule *rules =
		(struct fwstate_sync_rule *)memory_balloc(
			memory_context,
			sizeof(struct fwstate_sync_rule) * rule_count
		);
	if (rules == NULL) {
		return -1;
	}
	memset(rules, 0, sizeof(struct fwstate_sync_rule) * rule_count);

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct fwstate_sync_rule_input *input = inputs + rule_idx;
		struct fwstate_sync_rule *rule = rules + rule_idx;

		if (input->net4_count > 0) {
			rule->net4s.items = (struct net4 *)memory_balloc(
				memory_context,
				sizeof(struct net4) * input->net4_count
			);
			if (rule->net4s.items == NULL) {
				goto error;
			}
			memcpy(rule->net4s.items,
			       input->net4s,
			       sizeof(struct net4) * input->net4_count);
			rule->net4s.count = input->net4_count;
		}
		if (input->net6_count > 0) {
			rule->net6s.items = (struct net6 *)memory_balloc(
				memory_context,
				sizeof(struct net6) * input->net6_count
			);
			if (rule->net6s.items == NULL) {
				goto error;
			}
			memcpy(rule->net6s.items,
			       input->net6s,
			       sizeof(struct net6) * input->net6_count);
			rule->net6s.count = input->net6_count;
		}
		if (input->src_port_count > 0) {
			rule->src_ports.items =
				(const struct filter_u16_span *)memory_balloc(
					memory_context,
					sizeof(struct filter_u16_span) *
						input->src_port_count
				);
			if (rule->src_ports.items == NULL) {
				goto error;
			}
			memcpy((void *)rule->src_ports.items,
			       input->src_ports,
			       sizeof(struct filter_u16_span) *
				       input->src_port_count);
			rule->src_ports.count = input->src_port_count;
		}
	}

	const struct classifier_rule *rule_ptrs[FWSTATE_SYNC_RULES_MAX];
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		rule_ptrs[rule_idx] = &rules[rule_idx].rule;
	}

	struct fwstate_scope *scope = &config->scope;
	if (classify_fwstate_net4_compile(
		    memory_context,
		    "scope4",
		    rule_ptrs,
		    rule_count,
		    &scope->net4_attr,
		    &scope->net4_cls
	    )) {
		goto error;
	}
	if (classify_fwstate_net6_compile(
		    memory_context,
		    "scope6",
		    rule_ptrs,
		    rule_count,
		    &scope->net6_attr,
		    &scope->net6_cls
	    )) {
		goto error;
	}
	if (classify_fwstate_port_compile(
		    memory_context,
		    "scopep",
		    rule_ptrs,
		    rule_count,
		    &scope->port_attr,
		    &scope->port_cls
	    )) {
		goto error;
	}
	if (classify_join_rules(
		    memory_context,
		    "scope4",
		    &scope->net4_cls,
		    &scope->port_cls,
		    rule_ptrs,
		    rule_count,
		    &scope->net4_rules
	    )) {
		goto error;
	}
	if (classify_join_rules(
		    memory_context,
		    "scope6",
		    &scope->net6_cls,
		    &scope->port_cls,
		    rule_ptrs,
		    rule_count,
		    &scope->net6_rules
	    )) {
		goto error;
	}

	config->rules = rules;
	config->rule_count = rule_count;
	return 0;

error:
	// The partial scope unwinds through the scope free; the rules stay
	// in the config's memory tree, freed with the config itself.
	fwstate_scope_free(memory_context, &config->scope, rule_count);
	return -1;
}

// One-shot construction: install the caller's sync config (or the
// defaults) and declare the named map-object links. A config handle is
// built exactly once and never updated afterwards.
static int
fwstate_module_setup(
	struct fwstate_module_config *config,
	const struct fwstate_sync_config *sync_config,
	const struct fwstate_sync_rule_input *rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
) {
	struct cp_module *cp_module = &config->cp_module;

	if (sync_config != NULL) {
		config->sync_config = *sync_config;
	}

	if (fwstate_scope_compile(
		    &cp_module->entity.memory_context, config, rules, rule_count
	    )) {
		return -1;
	}

	if (fw4_map_name != NULL && fw4_map_name[0] != '\0') {
		if (fwstate_module_link_table(
			    cp_module,
			    fw4_map_name,
			    false,
			    &config->v4_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	if (fw6_map_name != NULL && fw6_map_name[0] != '\0') {
		if (fwstate_module_link_table(
			    cp_module,
			    fw6_map_name,
			    true,
			    &config->v6_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}

struct cp_module *
fwstate_module_config_new(
	struct agent *agent,
	const char *name,
	const struct fwstate_sync_config *sync_config,
	const struct fwstate_sync_rule_input *rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
) {
	struct fwstate_module_config *config =
		(struct fwstate_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct fwstate_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (rule_count > FWSTATE_SYNC_RULES_MAX) {
		yanet_error_add(
			err,
			"at most %d sync scope rules are supported",
			FWSTATE_SYNC_RULES_MAX
		);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct fwstate_module_config)
		);
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module, agent, FWSTATE_MODULE_NAME, name, err
	    )) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct fwstate_module_config)
		);
		return NULL;
	}
	// balloc does not zero, and a reader before any link is declared
	// must see both slots marked absent.
	config->v4_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
	config->v6_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
	config->worker_count = 0;
	SET_OFFSET_OF(&config->positions, NULL);
	memset(&config->scope, 0, sizeof(config->scope));
	SET_OFFSET_OF(&config->rules, NULL);
	config->rule_count = 0;
	fwstate_config_set_defaults(&config->sync_config);

	// Register module-level counters.
	// size=2 counters hold [packets, bytes]; size=1 counters hold
	// [packets].
	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"fwstate_sync", 2, &config->sync_packets_counter_id},
		{"fwstate_passthrough", 2, &config->passthrough_counter_id},
		{"fwstate_sync_v4_inserted",
		 1,
		 &config->sync_v4_inserted_counter_id},
		{"fwstate_sync_v6_inserted",
		 1,
		 &config->sync_v6_inserted_counter_id},
		{"fwstate_sync_v4_insert_failed",
		 1,
		 &config->sync_v4_insert_failed_counter_id},
		{"fwstate_sync_v6_insert_failed",
		 1,
		 &config->sync_v6_insert_failed_counter_id},
		{"fwstate_sync_v4_suppressed",
		 1,
		 &config->sync_v4_suppressed_counter_id},
		{"fwstate_sync_v6_suppressed",
		 1,
		 &config->sync_v6_suppressed_counter_id},
		{"fwstate_external_dropped",
		 2,
		 &config->external_dropped_counter_id},
		{"fwstate_internal_forwarded",
		 2,
		 &config->internal_forwarded_counter_id},
		{"fwstate_sync_alloc_failed",
		 1,
		 &config->sync_alloc_failed_counter_id},
		{"fwstate_scope_dropped", 2, &config->scope_dropped_counter_id},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[i].name
			);
			// Frees directly instead of going through the type
			// destructor.
			//
			// A failed configuration-data setup never reaches a
			// state its own teardown could safely walk. No
			// reference beyond the caller's own has been taken, and
			// no registry has observed the module yet, so nothing
			// is lost by freeing the block here.
			cp_module_fini(&config->cp_module);
			memory_bfree(
				&agent->memory_context,
				config,
				sizeof(struct fwstate_module_config)
			);
			return NULL;
		}
		*counters[i].dst = id;
	}

	if (fwstate_module_positions_new(config, agent)) {
		yanet_error_add(err, "failed to allocate stash read positions");
		fwstate_module_config_destroy(&config->cp_module);
		return NULL;
	}

	// A config handle is built exactly once: the sync config is
	// installed and the map-object links declared before the module is
	// ever visible to a registry. A failure tears the whole module down,
	// including the per-worker read positions; the config owns no table
	// memory.
	if (fwstate_module_setup(
		    config,
		    sync_config,
		    rules,
		    rule_count,
		    fw4_map_name,
		    fw6_map_name,
		    err
	    )) {
		fwstate_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

static void
fwstate_module_config_destroy(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	// The linked fwstate-map objects own the tables and the stashes; the
	// config owns its read positions, its scope rules and the compiled
	// scope.
	fwstate_scope_free(
		&cp_module->entity.memory_context,
		&config->scope,
		config->rule_count
	);
	fwstate_module_positions_free(config);
	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct fwstate_module_config)
	);
}

int
fwstate_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_module->agent);

	fwstate_module_config_destroy(cp_module);
	agent_destroy_released(owner);
	return 0;
}

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return config->sync_config;
}
