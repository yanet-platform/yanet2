// Classifier object: the create APIs of the subtypes, the subtype
// selection of the per-context handles, and the module binding.
//
// Pins the compiled bundles through the public handler - a batch of
// packets resolves to rule indices of the object's ruleset - and the
// binding contract end to end: a mirror module links the object,
// resolves the executing device's class through the object's device
// table, and its execution context carries the object's prepared
// handles, through which the same classification runs.

#include <arpa/inet.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include <rte_ether.h>

#include "api/agent.h"
#include "common/memory.h"
#include "common/strutils.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "devices/plain/api/controlplane.h"
#include "modules/mirror/api/controlplane.h"
#include "modules/mirror/dataplane/config.h"

#include "objects/classifier/api/classifier_lookup.h"
#include "objects/classifier/api/classifier_object.h"

#define CLASSIFIER_TEST_MEMORY_LIMIT (8u * 1024u * 1024u)

// One installed classifier object and the per-worker context the
// generation built for it.
struct classifier_test_object {
	struct cp_object *cp_object;
	struct object_ectx *object_ectx;
	struct classifier_prepared *prepared;
};

// Allocates and installs a classifier object, and resolves its
// worker-0 execution context and prepared buffer.
static int
classifier_test_install(
	struct agent *agent,
	const char *name,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	bool l3_subtype,
	struct classifier_test_object *installed,
	yanet_error **err
) {
	installed->cp_object =
		l3_subtype ? classifier_object_config_new_l3(
				     agent, name, rules, rule_count, err
			     )
			   : classifier_object_config_new_l2(
				     agent, name, rules, rule_count, err
			     );
	TEST_ASSERT_NOT_NULL(
		installed->cp_object,
		"create of '%s' failed: %s",
		name,
		*err ? yanet_error_message(*err) : "?"
	);

	struct cp_object *objects[] = {installed->cp_object};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 1, objects, err),
		"update_objects failed: %s",
		*err ? yanet_error_message(*err) : "?"
	);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(
			gen, CLASSIFIER_OBJECT_TYPE, name, &idx
		),
		"installed object must be findable"
	);
	installed->object_ectx = config_gen_ectx_get_object(ectx, idx);
	TEST_ASSERT_NOT_NULL(
		installed->object_ectx,
		"object context of '%s' must exist",
		name
	);
	installed->prepared = installed->object_ectx->abs_object_prepared;
	TEST_ASSERT_NOT_NULL(
		installed->prepared,
		"the classifier object must declare a prepared buffer"
	);

	return TEST_SUCCESS;
}

// A vlan line range view over one static interval.
static struct classify_line_ranges
classifier_test_vlan_ranges(
	struct classify_line_range *range, uint16_t from, uint16_t to
) {
	range->from = from;
	range->to = to;
	return (struct classify_line_ranges){.items = range, .count = 1};
}

// A net4 set view over one static network.
static struct filter_net4s
classifier_test_net4s(struct net4 *net, const uint8_t addr[4], uint8_t prefix) {
	memset(net, 0, sizeof(*net));
	memcpy(net->addr, addr, 4);
	memset(net->mask, 0xff, prefix / 8);
	if (prefix % 8) {
		net->mask[prefix / 8] = (uint8_t)(0xff << (8 - prefix % 8));
	}
	return (struct filter_net4s){.items = net, .count = 1};
}

// A net6 set view over one static network.
static struct filter_net6s
classifier_test_net6s(
	struct net6 *net, const uint8_t addr[NET6_LEN], uint8_t prefix
) {
	memset(net, 0, sizeof(*net));
	memcpy(net->addr, addr, NET6_LEN);
	memset(net->mask, 0xff, prefix / 8);
	if (prefix % 8) {
		net->mask[prefix / 8] = (uint8_t)(0xff << (8 - prefix % 8));
	}
	return (struct filter_net6s){.items = net, .count = 1};
}

// An l2-path packet: the family ethertype cleared so no family path
// applies, the vlan id set directly the way the dataplane parser
// would.
static int
classifier_test_make_l2_packet(struct packet *packet, uint16_t vlan) {
	const uint8_t any4[4] = {0, 0, 0, 0};
	TEST_ASSERT_SUCCESS(
		fill_packet_net4(packet, any4, any4, 1, 2, IPPROTO_UDP, 0),
		"packet fill failed"
	);
	packet->network_header.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_ARP);
	packet->vlan = vlan;
	return TEST_SUCCESS;
}

// The l2 subtype through the public handler: the vlan and device
// conditions resolve to rule indices, a device outside the rules
// resolves through the any slot, and an unmatched vlan keeps the
// invalid mark.
static int
run_classifier_l2_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm, 0, "classifier-l2", CLASSIFIER_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct filter_device rule0_devices[1];
	memset(rule0_devices, 0, sizeof(rule0_devices));
	strtcpy(rule0_devices[0].name, "dev0", CP_DEVICE_NAME_LEN);

	struct classify_line_range rule_vlans[2];

	struct classifier_object_rule rules[2];
	memset(rules, 0, sizeof(rules));
	rules[0].devices =
		(struct filter_devices){.items = rule0_devices, .count = 1};
	rules[0].vlan_ranges =
		classifier_test_vlan_ranges(&rule_vlans[0], 100, 100);
	rules[1].vlan_ranges =
		classifier_test_vlan_ranges(&rule_vlans[1], 200, 200);

	struct classifier_test_object installed;
	TEST_ASSERT_SUCCESS(
		classifier_test_install(
			agent, "l2", rules, 2, false, &installed, &err
		),
		"install failed"
	);
	TEST_ASSERT_EQUAL(
		(long)installed.prepared->subtype,
		(long)CLASSIFIER_OBJECT_SUBTYPE_L2,
		"the prepared buffer must carry the l2 subtype"
	);
	TEST_ASSERT(
		installed.prepared->classify_batch != NULL,
		"the l2 subtype must select a classify handle"
	);
	TEST_ASSERT_EQUAL(
		(long)classifier_object_rule_count(installed.cp_object),
		2L,
		"the object must report its ruleset size"
	);
	TEST_ASSERT_EQUAL(
		(long)classifier_object_device_count(installed.cp_object),
		2L,
		"the device table holds the any slot and dev0"
	);
	TEST_ASSERT(
		strcmp(classifier_object_device_name(installed.cp_object, 1),
		       "dev0") == 0,
		"the first rule device follows the any slot"
	);

	struct packet storage[3];
	struct packet *packets[3] = {&storage[0], &storage[1], &storage[2]};
	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packets[0], 100),
		"l2 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packets[1], 200),
		"l2 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packets[2], 300),
		"l2 packet fill failed"
	);

	uint32_t results[3];
	classifier_object_classify(
		installed.prepared,
		classifier_object_device_class(installed.cp_object, "dev0"),
		(const struct packet **)packets,
		results,
		3
	);
	TEST_ASSERT_EQUAL((long)results[0], 0L, "vlan 100 on dev0 is rule 0");
	TEST_ASSERT_EQUAL((long)results[1], 1L, "vlan 200 is rule 1");
	TEST_ASSERT(
		results[2] == CLASSIFY_RULE_INVALID, "vlan 300 matches no rule"
	);

	// A device the rules never named resolves through the any slot:
	// rule 0 carries a device set, so it stops matching.
	classifier_object_classify(
		installed.prepared,
		classifier_object_device_class(installed.cp_object, "dev9"),
		(const struct packet **)packets,
		results,
		3
	);
	TEST_ASSERT(
		results[0] == CLASSIFY_RULE_INVALID,
		"rule 0 must not match on a device outside its set"
	);
	TEST_ASSERT_EQUAL(
		(long)results[1],
		1L,
		"rule 1 has no device condition and keeps matching"
	);

	for (size_t idx = 0; idx < 3; ++idx) {
		free_packet(packets[idx]);
	}

	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, CLASSIFIER_OBJECT_TYPE, "l2", &err),
		"delete failed"
	);
	TEST_ASSERT_SUCCESS(
		classifier_object_config_free(installed.cp_object, &err),
		"free failed"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// The l3 subtype through the public handler: the l2, the net4 and the
// net6 paths resolve their own projections, a packet of both the l2
// and a family path keeps the first matching rule, and a batch over
// the chunk boundary classifies every packet.
static int
run_classifier_l3_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm, 0, "classifier-l3", CLASSIFIER_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct classify_line_range l2_vlan;
	struct net4 net4;
	struct net6 net6;

	struct classifier_object_rule rules[3];
	memset(rules, 0, sizeof(rules));
	rules[0].vlan_ranges = classifier_test_vlan_ranges(&l2_vlan, 100, 100);
	rules[1].src_net4s = classifier_test_net4s(
		&net4, (const uint8_t[]){127, 0, 0, 0}, 24
	);
	rules[1].dst_net4s = rules[1].src_net4s;
	{
		const uint8_t addr[NET6_LEN] = {[0] = 0xfe, [1] = 0x80};
		rules[2].src_net6s = classifier_test_net6s(&net6, addr, 16);
	}
	rules[2].dst_net6s = rules[2].src_net6s;

	struct classifier_test_object installed;
	TEST_ASSERT_SUCCESS(
		classifier_test_install(
			agent, "l3", rules, 3, true, &installed, &err
		),
		"install failed"
	);

	TEST_ASSERT_EQUAL(
		(long)installed.prepared->subtype,
		(long)CLASSIFIER_OBJECT_SUBTYPE_L3,
		"the prepared buffer must carry the l3 subtype"
	);
	TEST_ASSERT(
		installed.prepared->classify_batch != NULL,
		"the l3 subtype must select a classify handle"
	);
	TEST_ASSERT(
		installed.prepared->object == container_of(
						      installed.cp_object,
						      struct classifier_object,
						      cp_object
					      ),
		"the prepared buffer must point at the installed object"
	);

	struct packet storage[6];
	struct packet *packets[6];
	for (size_t idx = 0; idx < 6; ++idx) {
		packets[idx] = &storage[idx];
	}
	const uint8_t local4[4] = {127, 0, 0, 1};
	const uint8_t remote4[4] = {10, 0, 0, 1};
	const uint8_t local6[NET6_LEN] = {[0] = 0xfe, [1] = 0x80};
	const uint8_t remote6[NET6_LEN] = {[0] = 0x20, [1] = 0x01};

	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packets[0], 100),
		"l2 packet fill failed"
	);

	TEST_ASSERT_SUCCESS(
		fill_packet_net4(
			packets[1], local4, local4, 1, 2, IPPROTO_UDP, 0
		),
		"net4 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		fill_packet_net4(
			packets[3], remote4, remote4, 1, 2, IPPROTO_UDP, 0
		),
		"net4 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		fill_packet_net4(
			packets[4], local4, local4, 1, 2, IPPROTO_UDP, 0
		),
		"net4 packet fill failed"
	);
	packets[4]->vlan = 100;

	TEST_ASSERT_SUCCESS(
		fill_packet_net6(
			packets[2], local6, local6, 1, 2, IPPROTO_UDP, 0
		),
		"net6 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		fill_packet_net6(
			packets[5], remote6, remote6, 1, 2, IPPROTO_UDP, 0
		),
		"net6 packet fill failed"
	);

	const uint32_t device_class =
		classifier_object_device_class(installed.cp_object, "dev0");

	uint32_t results[6];
	classifier_object_classify(
		installed.prepared,
		device_class,
		(const struct packet **)packets,
		results,
		6
	);

	const uint32_t expected[6] = {
		0,
		1,
		2,
		CLASSIFY_RULE_INVALID,
		0,
		CLASSIFY_RULE_INVALID,
	};
	for (size_t idx = 0; idx < 6; ++idx) {
		TEST_ASSERT(
			results[idx] == expected[idx],
			"packet %zu classified as %u, expected %u",
			idx,
			results[idx],
			expected[idx]
		);
	}

	// A batch over the chunk boundary: every packet classifies, the
	// chunk loop loses no result.
#define CLS_L3_BATCH (CLS_CLASSIFY_MAX_BATCH + 44)
	static struct packet batch_storage[CLS_L3_BATCH];
	static struct packet *batch_packets[CLS_L3_BATCH];
	for (uint32_t idx = 0; idx < CLS_L3_BATCH; ++idx) {
		batch_packets[idx] = &batch_storage[idx];
		TEST_ASSERT_SUCCESS(
			fill_packet_net4(
				batch_packets[idx],
				local4,
				local4,
				1,
				2,
				IPPROTO_UDP,
				0
			),
			"net4 packet fill failed"
		);
	}
	static uint32_t batch_results[CLS_L3_BATCH];
	classifier_object_classify(
		installed.prepared,
		device_class,
		(const struct packet **)batch_packets,
		batch_results,
		CLS_L3_BATCH
	);
	for (uint32_t idx = 0; idx < CLS_L3_BATCH; ++idx) {
		TEST_ASSERT(
			batch_results[idx] == 1,
			"batch packet %u classified as %u, expected 1",
			idx,
			batch_results[idx]
		);
	}
	for (uint32_t idx = 0; idx < CLS_L3_BATCH; ++idx) {
		free_packet(batch_packets[idx]);
	}

	for (size_t idx = 0; idx < 6; ++idx) {
		free_packet(packets[idx]);
	}

	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, CLASSIFIER_OBJECT_TYPE, "l3", &err),
		"delete failed"
	);
	TEST_ASSERT_SUCCESS(
		classifier_object_config_free(installed.cp_object, &err),
		"free failed"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// The l2 create rejects rules carrying network conditions, and the
// refusal leaves nothing allocated.
static int
run_l2_rejects_network_rules_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm,
		0,
		"classifier-l2-reject",
		CLASSIFIER_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct net4 net4;
	struct classifier_object_rule rules[1];
	memset(rules, 0, sizeof(rules));
	rules[0].src_net4s = classifier_test_net4s(
		&net4, (const uint8_t[]){127, 0, 0, 0}, 24
	);
	rules[0].dst_net4s = rules[0].src_net4s;

	struct cp_object *cp_object =
		classifier_object_config_new_l2(agent, "bad", rules, 1, &err);
	TEST_ASSERT(
		cp_object == NULL,
		"the l2 create must reject a rule with network conditions"
	);
	char *refusal = yanet_error_format(err);
	TEST_ASSERT_STR_CONTAINS(
		refusal,
		"network conditions",
		"the refusal must name the rejected conditions"
	);
	free(refusal);
	yanet_error_free(err);
	err = NULL;

	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"the refused create must leave the arena unchanged"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A buffer no commit pass selected classifies nothing: the public
// handler's subtype check resolves every packet to no rule.
static int
run_unselected_handle_classifies_nothing_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm,
		0,
		"classifier-unselected",
		CLASSIFIER_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct classify_line_range vlan;
	struct classifier_object_rule rules[1];
	memset(rules, 0, sizeof(rules));
	rules[0].vlan_ranges = classifier_test_vlan_ranges(&vlan, 100, 100);

	struct classifier_test_object installed;
	TEST_ASSERT_SUCCESS(
		classifier_test_install(
			agent, "unselected", rules, 1, true, &installed, &err
		),
		"install failed"
	);

	struct classifier_prepared unselected = {0};
	struct packet packet;
	struct packet *packet_ptr = &packet;
	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packet_ptr, 100),
		"l2 packet fill failed"
	);

	uint32_t result = 42;
	classifier_object_classify(
		&unselected, 0, (const struct packet **)&packet_ptr, &result, 1
	);
	TEST_ASSERT(
		result == CLASSIFY_RULE_INVALID,
		"an unselected handle must resolve to no rule"
	);

	free_packet(&packet);

	TEST_ASSERT_SUCCESS(
		agent_delete_object(
			agent, CLASSIFIER_OBJECT_TYPE, "unselected", &err
		),
		"delete failed"
	);
	TEST_ASSERT_SUCCESS(
		classifier_object_config_free(installed.cp_object, &err),
		"free failed"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Walks the context tree along the control-plane-owned relative
// arrays and returns the module context registered under the given
// type and name, or NULL when no stage carries it.
static struct module_ectx *
classifier_test_find_module_ectx(
	struct config_gen_ectx *ectx, const char *type, const char *name
) {
	struct device_ectx **device_ptrs = ADDR_OF(&ectx->device_ptrs);
	for (uint64_t dev_idx = 0; dev_idx < ectx->device_count; ++dev_idx) {
		struct device_ectx *device_ectx =
			ADDR_OF(device_ptrs + dev_idx);
		if (device_ectx == NULL) {
			continue;
		}
		for (uint64_t dir = 0; dir < 2; ++dir) {
			struct device_entry_ectx *entry =
				ADDR_OF(dir ? &device_ectx->output_pipelines
					    : &device_ectx->input_pipelines);
			if (entry == NULL) {
				continue;
			}
			struct pipeline_ectx **pipeline_ptrs =
				ADDR_OF(&entry->pipeline_ptrs);
			for (uint64_t pipe_idx = 0;
			     pipe_idx < entry->pipeline_count;
			     ++pipe_idx) {
				struct pipeline_ectx *pipeline_ectx =
					ADDR_OF(pipeline_ptrs + pipe_idx);
				if (pipeline_ectx == NULL) {
					continue;
				}
				struct function_ectx **function_ptrs =
					ADDR_OF(&pipeline_ectx->function_ptrs);
				for (uint64_t func_idx = 0;
				     func_idx < pipeline_ectx->length;
				     ++func_idx) {
					struct function_ectx *function_ectx =
						ADDR_OF(function_ptrs +
							func_idx);
					if (function_ectx == NULL) {
						continue;
					}
					struct chain_ectx **chain_ptrs =
						ADDR_OF(&function_ectx
								 ->chain_ptrs);
					for (uint64_t chain_idx = 0;
					     chain_idx <
					     function_ectx->chain_count;
					     ++chain_idx) {
						struct chain_ectx *chain_ectx =
							ADDR_OF(chain_ptrs +
								chain_idx);
						if (chain_ectx == NULL) {
							continue;
						}
						struct module_ectx *
							*module_ptrs = ADDR_OF(
								&chain_ectx
									 ->module_ptrs
							);
						for (uint64_t mod_idx = 0;
						     mod_idx <
						     chain_ectx->length;
						     ++mod_idx) {
							struct module_ectx *
								candidate = ADDR_OF(
									module_ptrs +
									mod_idx
								);
							if (candidate == NULL) {
								continue;
							}
							struct cp_module *
								cp_module = ADDR_OF(
									&candidate
										 ->cp_module
								);
							if (!strncmp(
								    cp_module
									    ->type,
								    type,
								    CP_MODULE_TYPE_LEN
							    ) &&
							    !strncmp(
								    cp_module
									    ->name,
								    name,
								    CP_MODULE_NAME_LEN
							    )) {
								return candidate;
							}
						}
					}
				}
			}
		}
	}
	return NULL;
}

// The module binding contract: the module init requires the linked
// classifier object installed, validates its decision count against
// the object's ruleset, and a built module context carries the
// object's prepared handles and the executing device's class.
static int
run_module_binding_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		shm, 0, "classifier-binding", CLASSIFIER_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct classify_line_range l2_vlan;
	struct net4 net4;
	struct filter_device rule_devices[1];
	memset(rule_devices, 0, sizeof(rule_devices));
	strtcpy(rule_devices[0].name, "dev0", CP_DEVICE_NAME_LEN);

	struct classifier_object_rule rules[2];
	memset(rules, 0, sizeof(rules));
	rules[0].devices =
		(struct filter_devices){.items = rule_devices, .count = 1};
	rules[0].vlan_ranges = classifier_test_vlan_ranges(&l2_vlan, 100, 100);
	rules[1].src_net4s = classifier_test_net4s(
		&net4, (const uint8_t[]){127, 0, 0, 0}, 24
	);
	rules[1].dst_net4s = rules[1].src_net4s;

	struct mirror_decision decisions[2];
	memset(decisions, 0, sizeof(decisions));
	strtcpy(decisions[0].target, "dev0", CP_DEVICE_NAME_LEN);
	strtcpy(decisions[0].counter, "cnt0", COUNTER_NAME_LEN);
	decisions[0].mode = MIRROR_MODE_OUT;
	strtcpy(decisions[1].target, "dev0", CP_DEVICE_NAME_LEN);
	strtcpy(decisions[1].counter, "cnt1", COUNTER_NAME_LEN);
	decisions[1].mode = MIRROR_MODE_IN;

	// The module init must refuse an object that was never installed.
	{
		struct cp_module *module = mirror_module_config_init(
			agent, "mir-missing", "cls0", decisions, 2, &err
		);
		TEST_ASSERT(
			module == NULL,
			"the module init must require an installed object"
		);
		TEST_ASSERT_STR_CONTAINS(
			yanet_error_message(err),
			"classifier",
			"the refusal must name the missing object"
		);
		yanet_error_free(err);
		err = NULL;
	}

	struct classifier_test_object installed;
	TEST_ASSERT_SUCCESS(
		classifier_test_install(
			agent, "cls0", rules, 2, true, &installed, &err
		),
		"install failed"
	);

	// A decision count off the object's ruleset is refused.
	{
		struct cp_module *module = mirror_module_config_init(
			agent, "mir-count", "cls0", decisions, 3, &err
		);
		TEST_ASSERT(
			module == NULL,
			"the module init must refuse a decision count off "
			"the ruleset"
		);
		yanet_error_free(err);
		err = NULL;
	}

	struct cp_module *module = mirror_module_config_init(
		agent, "mir0", "cls0", decisions, 2, &err
	);
	TEST_ASSERT_NOT_NULL(
		module,
		"module init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *modules[] = {module};
	TEST_ASSERT_SUCCESS(
		cp_config_update_modules(
			dp_config, cp_config, 1, modules, &err
		),
		"update_modules failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// The pipeline wiring the module: function -> chain -> module,
	// pipeline, device. The device install builds the execution
	// contexts.
	const char *const chain_types[] = {"mirror"};
	const char *const chain_names[] = {"mir0"};
	struct cp_chain_config *chain_cfg =
		cp_chain_config_create("chain0", 1, chain_types, chain_names);
	TEST_ASSERT_NOT_NULL(chain_cfg, "chain_config_create failed");
	struct cp_function_config *func_cfg =
		cp_function_config_create("func0", 1);
	TEST_ASSERT_NOT_NULL(func_cfg, "function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(func_cfg, 0, chain_cfg, 1),
		"set_chain failed"
	);
	struct cp_function_config *func_cfgs[] = {func_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_functions(
			dp_config, cp_config, 1, func_cfgs, &err
		),
		"update_functions failed"
	);
	cp_function_config_free(func_cfg);

	struct cp_pipeline_config *pipe_cfg =
		cp_pipeline_config_create("pipe0", 1);
	TEST_ASSERT_NOT_NULL(pipe_cfg, "pipeline_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(pipe_cfg, 0, "func0"),
		"set_function failed"
	);
	struct cp_pipeline_config *pipe_cfgs[] = {pipe_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_pipelines(
			dp_config, cp_config, 1, pipe_cfgs, &err
		),
		"update_pipelines failed"
	);
	cp_pipeline_config_free(pipe_cfg);

	{
		struct cp_device_plain_config *dev_cfg =
			cp_device_plain_config_new("dev0", 1, 0, &err);
		TEST_ASSERT_NOT_NULL(dev_cfg, "device config create failed");
		cp_device_plain_config_set_input_pipeline(
			dev_cfg, 0, "pipe0", 1
		);
		struct cp_device *dev =
			cp_device_plain_new(agent, dev_cfg, &err);
		cp_device_plain_config_free(dev_cfg);
		TEST_ASSERT_NOT_NULL(dev, "device create failed");
		struct cp_device *devs[] = {dev};
		TEST_ASSERT_SUCCESS(
			cp_config_update_devices(
				dp_config, cp_config, 1, devs, &err
			),
			"update_devices failed"
		);
		yanet_error *free_err = NULL;
		int free_rc = cp_device_plain_free(dev, &free_err);
		yanet_error_free(free_err);
		TEST_ASSERT(
			free_rc == -1 && errno == EAGAIN,
			"the installed device must stay referenced"
		);
	}

	// The module context carries the object's prepared handles and
	// the executing device's class, resolved through the object's
	// device table. The prepared buffer is the one the live
	// generation built, re-resolved here: every install above replaced
	// the generation, and each generation carries its own object
	// contexts.
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	{
		uint64_t object_idx;
		TEST_ASSERT_SUCCESS(
			cp_config_gen_lookup_object_index(
				gen, CLASSIFIER_OBJECT_TYPE, "cls0", &object_idx
			),
			"the classifier object must be installed"
		);
		installed.object_ectx =
			config_gen_ectx_get_object(ectx, object_idx);
		installed.prepared = installed.object_ectx->abs_object_prepared;
	}

	struct module_ectx *module_ectx =
		classifier_test_find_module_ectx(ectx, "mirror", "mir0");
	TEST_ASSERT_NOT_NULL(module_ectx, "the mir0 module context must exist");

	struct mirror_prepared *mirror_prepared =
		ADDR_OF(&module_ectx->module_prepared);
	TEST_ASSERT(
		mirror_prepared->classifier == installed.prepared,
		"the module context must carry the linked object's prepared "
		"handles"
	);
	TEST_ASSERT_EQUAL(
		(long)mirror_prepared->device_class,
		(long)classifier_object_device_class(
			installed.cp_object, "dev0"
		),
		"the module context must carry its executing device's class"
	);

	// The classification through the module's carried handles is the
	// object's own: the module resolves the result, the decision
	// bound to the rule index.
	struct packet storage[2];
	struct packet *packets[2] = {&storage[0], &storage[1]};
	const uint8_t local4[4] = {127, 0, 0, 1};
	TEST_ASSERT_SUCCESS(
		classifier_test_make_l2_packet(packets[0], 100),
		"l2 packet fill failed"
	);
	TEST_ASSERT_SUCCESS(
		fill_packet_net4(
			packets[1], local4, local4, 1, 2, IPPROTO_UDP, 0
		),
		"net4 packet fill failed"
	);

	uint32_t results[2];
	classifier_object_classify(
		mirror_prepared->classifier,
		mirror_prepared->device_class,
		(const struct packet **)packets,
		results,
		2
	);
	TEST_ASSERT_EQUAL(
		(long)results[0],
		0L,
		"vlan 100 on dev0 resolves to rule 0 through the module's "
		"handles"
	);
	TEST_ASSERT_EQUAL(
		(long)results[1],
		1L,
		"the net4 pair resolves to rule 1 through the module's handles"
	);

	free_packet(packets[0]);
	free_packet(packets[1]);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *mods_to_load[] = {"mirror"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {"classifier"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = mods_to_load,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	struct {
		const char *name;
		int (*fn)(struct yanet_shm *);
	} tests[] = {
		{"classifier_l2", run_classifier_l2_test},
		{"classifier_l3", run_classifier_l3_test},
		{"classifier_l2_rejects_network_rules",
		 run_l2_rejects_network_rules_test},
		{"classifier_unselected_handle",
		 run_unselected_handle_classifies_nothing_test},
		{"classifier_module_binding", run_module_binding_test},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; ++idx) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn(shm) != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			++failed;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	dataplane_ut_free(ut);

	if (failed == 0) {
		LOG(INFO,
		    "=== All %zu classifier object tests passed! ===",
		    total);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu classifier object tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
