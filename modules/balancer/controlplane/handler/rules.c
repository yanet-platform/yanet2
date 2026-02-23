#include "rules.h"

#include "api/vs.h"
#include "common/memory.h"
#include "filter/compiler.h"
#include "filter/rule.h"
#include "handler.h"
#include "lib/controlplane/diag/diag.h"

#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

// Declare filter compiler signatures for VS lookup tables
FILTER_COMPILER_DECLARE(vs_lookup_ipv4, net4_fast_dst, port_dst, proto);
FILTER_COMPILER_DECLARE(vs_lookup_ipv6, net6_fast_dst, port_dst, proto);

static int
init_transport_rule(
	struct filter_rule *rule, struct named_vs_config *vs_config
) {
	rule->transport.dst_count = 1;
	rule->transport.dsts = calloc(1, sizeof(struct filter_port_range));

	// For PureL3 mode, match all ports (0-65535)
	// Otherwise, match only the specific port
	if (vs_config->config.flags & VS_PURE_L3_FLAG) {
		rule->transport.dsts[0].from = 0;
		rule->transport.dsts[0].to = 65535;
	} else {
		rule->transport.dsts[0].from = vs_config->identifier.port;
		rule->transport.dsts[0].to = vs_config->identifier.port;
	}

	if (vs_config->identifier.transport_proto != IPPROTO_TCP &&
	    vs_config->identifier.transport_proto != IPPROTO_UDP) {
		NEW_ERROR(
			"unsupported transport protocol %d: only TCP (%d) and "
			"UDP (%d) are supported",
			vs_config->identifier.transport_proto,
			IPPROTO_TCP,
			IPPROTO_UDP
		);
		return -1;
	}

	rule->transport.proto.proto = vs_config->identifier.transport_proto;
	rule->transport.proto.enable_bits = 0;
	rule->transport.proto.disable_bits = 0;
	return 0;
}

static void
init_dst_rule(struct filter_rule *rule, struct named_vs_config *vs_config) {
	memset(&rule->net6, 0, sizeof(rule->net6));
	memset(&rule->net4, 0, sizeof(rule->net4));
	if (vs_config->identifier.ip_proto == IPPROTO_IPV6) {
		rule->net6.dst_count = 1;
		rule->net6.dsts = malloc(sizeof(struct net6));
		struct net6 *n = &rule->net6.dsts[0];
		memcpy(n->addr, vs_config->identifier.addr.v6.bytes, NET6_LEN);
		memset(n->mask, 0xFF, NET6_LEN);
	} else { // ipv4
		rule->net4.dst_count = 1;
		rule->net4.dsts = malloc(sizeof(struct net4));
		struct net4 *n = &rule->net4.dsts[0];
		memcpy(n->addr, vs_config->identifier.addr.v4.bytes, NET4_LEN);
		memset(n->mask, 0xFF, NET4_LEN);
	}
}

int
make_filter_rules(
	struct filter_rule **result_rules,
	size_t count,
	struct named_vs_config *vs_configs,
	size_t *vs_initial_idx
) {
	*result_rules = NULL;
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * count);
	for (size_t rule_idx = 0; rule_idx < count; ++rule_idx) {
		const size_t vs_idx = rule_idx;
		init_dst_rule(rules + rule_idx, vs_configs + vs_idx);
		if (init_transport_rule(
			    rules + rule_idx, vs_configs + vs_idx
		    ) != 0) {
			free(rules);
			PUSH_ERROR(
				"service at index %zu", vs_initial_idx[vs_idx]
			);
			return -1;
		}
		rules[rule_idx].action = rule_idx;
	}
	*result_rules = rules;
	return 0;
}

void
free_rules(size_t rules_count, struct filter_rule *rules) {
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct filter_rule *rule = rules + rule_idx;
		free(rule->net4.dsts);
		free(rule->net6.dsts);
		free(rule->transport.dsts);
	}
	free(rules);
}

int
build_filter(
	struct packet_handler_vs *packet_handler_vs,
	size_t *initial_vs_idx,
	struct named_vs_config *vs_configs,
	struct memory_context *mctx,
	int proto
) {
	struct filter *filter = memory_balloc(mctx, sizeof(struct filter));
	if (filter == NULL) {
		NEW_ERROR("no memory");
		return -1;
	}

	struct filter_rule *rules = NULL;
	const size_t vs_count = packet_handler_vs->vs_count;
	if (make_filter_rules(&rules, vs_count, vs_configs, initial_vs_idx) !=
	    0) {
		PUSH_ERROR("invalid VS configs");
		memory_bfree(mctx, filter, sizeof(struct filter));
		return -1;
	}

	const size_t rules_count = vs_count;
	if (proto == IPPROTO_IPV6) {
		if (FILTER_INIT(
			    filter, vs_lookup_ipv6, rules, rules_count, mctx
		    ) != 0) {
			memory_bfree(mctx, filter, sizeof(struct filter));
			free_rules(rules_count, rules);
			NEW_ERROR("no memory");
			return -1;
		}
	} else {
		if (FILTER_INIT(
			    filter, vs_lookup_ipv4, rules, rules_count, mctx
		    ) != 0) {
			memory_bfree(mctx, filter, sizeof(struct filter));
			free_rules(rules_count, rules);
			NEW_ERROR("no memory");
			return -1;
		}
	}

	SET_OFFSET_OF(&packet_handler_vs->filter, filter);

	free_rules(rules_count, rules);

	return 0;
}

uint64_t
rules_memory_usage(size_t rules_count, struct filter_rule *rules) {
	uint64_t result = sizeof(struct filter_rule) * rules_count;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct filter_rule *rule = rules + rule_idx;
		result += sizeof(struct net6) *
			  (rule->net6.dst_count + rule->net6.src_count);
		result += sizeof(struct net4) *
			  (rule->net4.dst_count + rule->net4.src_count);
		result +=
			sizeof(struct filter_port_range) *
			(rule->transport.dst_count + rule->transport.src_count);
		result += sizeof(struct filter_proto_range) *
			  rule->transport.proto_count;
	}
	return result;
}