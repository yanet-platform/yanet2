#pragma once

#include <stddef.h>
#include <stdint.h>

struct filter_rule;
struct named_vs_config;
struct packet_handler_vs;
struct memory_context;

/**
 * Create filter rules from VS configurations.
 *
 * @param result_rules Output pointer to allocated filter rules array
 * @param count Number of VS configurations
 * @param vs_configs Array of VS configurations
 * @param vs_initial_idx Array of initial VS indices for error reporting
 * @return 0 on success, -1 on error
 */
int
make_filter_rules(
	struct filter_rule **result_rules,
	size_t count,
	struct named_vs_config *vs_configs,
	size_t *vs_initial_idx
);

/**
 * Free filter rules and their associated resources.
 *
 * @param rules_count Number of rules to free
 * @param rules Array of filter rules
 */
void
free_rules(size_t rules_count, struct filter_rule *rules);

/**
 * Build a filter from rules for a packet handler VS.
 *
 * @param packet_handler_vs Packet handler VS structure
 * @param initial_vs_idx Array of initial VS indices for error reporting
 * @param vs_configs Array of VS configurations
 * @param mctx Memory context for allocations
 * @param proto IP protocol (IPPROTO_IP or IPPROTO_IPV6)
 * @return 0 on success, -1 on error
 */
int
build_filter(
	struct packet_handler_vs *packet_handler_vs,
	size_t *initial_vs_idx,
	struct named_vs_config *vs_configs,
	struct memory_context *mctx,
	int proto
);

// TODO: docs
uint64_t
rules_memory_usage(size_t rules_count, struct filter_rule *rules);