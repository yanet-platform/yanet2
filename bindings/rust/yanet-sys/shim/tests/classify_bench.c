// The inline C query the classify benchmark of the SDK measures the Rust ones
// against: the per-key query a C module composes from the header-inline
// lookups of lib/classify, compiled by the dataplane's compiler and flags.
#include <stdint.h>

#include "common/value.h"

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/query.h"
#include "lib/classify/rule_table.h"

// The parts of a classifier of two networks and two ports: the networks
// joined, the ports joined, the two joints joined at the root, and the rule
// map of the root's classes.
struct yanet_sys_bench_query {
	const struct classify_attr_net4 *src;
	const struct classify_attr_net4 *dst;
	const struct classify_attr_port *sport;
	const struct classify_attr_port *dport;
	const struct value_table *nets;
	const struct value_table *ports;
	const struct value_table *root;
	const struct vline *rules;
};

// A key of the query: the addresses in network order, the ports in host
// order.
struct yanet_sys_bench_key {
	uint32_t src;
	uint32_t dst;
	uint16_t sport;
	uint16_t dport;
};

static inline uint32_t
query(const struct yanet_sys_bench_query *q,
      const struct yanet_sys_bench_key *key) {
	uint32_t src, dst, nets, ports, rule;
	classify_net4_lookup(q->src, &key->src, &src, 1);
	classify_net4_lookup(q->dst, &key->dst, &dst, 1);
	uint32_t sport = vline_get((struct vline *)&q->sport->line, key->sport);
	uint32_t dport = vline_get((struct vline *)&q->dport->line, key->dport);
	classify_joint_lookup(q->nets, &src, &dst, &nets, 1);
	classify_joint_lookup(q->ports, &sport, &dport, &ports, 1);
	classify_combine(q->root, q->rules, &nets, &ports, &rule, 1);
	return rule;
}

// Queries each key once per round, one key per query, and returns the sum
// of the rules; the keys are read again each round.
uint64_t
yanet_sys_bench_c_run(
	const struct yanet_sys_bench_query *q,
	const struct yanet_sys_bench_key *keys,
	uint32_t count,
	uint64_t rounds
) {
	uint64_t sum = 0;
	for (uint64_t round = 0; round < rounds; ++round) {
		__asm__ volatile("" ::: "memory");
		for (uint32_t idx = 0; idx < count; ++idx) {
			sum += query(q, keys + idx);
		}
	}
	return sum;
}

// As yanet_sys_bench_c_run, with the batch query a C module runs over a
// burst: each lookup over the whole burst of 32 keys before the next, the
// fields of the keys gathered first as a module gathers them from packets.
uint64_t
yanet_sys_bench_c_run_burst(
	const struct yanet_sys_bench_query *q,
	const struct yanet_sys_bench_key *keys,
	uint32_t count,
	uint64_t rounds
) {
	uint64_t sum = 0;
	for (uint64_t round = 0; round < rounds; ++round) {
		__asm__ volatile("" ::: "memory");
		for (uint32_t start = 0; start < count; start += 32) {
			uint32_t n = count - start < 32 ? count - start : 32;
			uint32_t src_addrs[32], dst_addrs[32];
			uint32_t src[32], dst[32], sport[32], dport[32];
			uint32_t nets[32], ports[32], rules[32];
			for (uint32_t idx = 0; idx < n; ++idx) {
				src_addrs[idx] = keys[start + idx].src;
				dst_addrs[idx] = keys[start + idx].dst;
			}
			classify_net4_lookup(q->src, src_addrs, src, n);
			classify_net4_lookup(q->dst, dst_addrs, dst, n);
			for (uint32_t idx = 0; idx < n; ++idx) {
				sport[idx] = vline_get(
					(struct vline *)&q->sport->line,
					keys[start + idx].sport
				);
				dport[idx] = vline_get(
					(struct vline *)&q->dport->line,
					keys[start + idx].dport
				);
			}
			classify_joint_lookup(q->nets, src, dst, nets, n);
			classify_joint_lookup(q->ports, sport, dport, ports, n);
			classify_combine(
				q->root, q->rules, nets, ports, rules, n
			);
			for (uint32_t idx = 0; idx < n; ++idx) {
				sum += rules[idx];
			}
		}
	}
	return sum;
}

// The parts of a classifier on two protocol paths, after the UDP path of
// the ACL module: the networks joined into a core every path shares, the
// ports of the rules that restrict one joined, and per path a rule table of
// the core and the ports and a rule map of the core alone.
struct yanet_sys_bench_paths {
	const struct classify_attr_net4 *src;
	const struct classify_attr_net4 *dst;
	const struct classify_attr_port *sport;
	const struct classify_attr_port *dport;
	const struct value_table *nets;
	const struct value_table *ports;
	const struct rule_table *tables[2];
	const struct vline *maps[2];
};

// A key of the paths: the fields of a key and the path of its protocol.
struct yanet_sys_bench_path_key {
	struct yanet_sys_bench_key key;
	uint32_t path;
};

// The earlier of the rules of a path's table and map, the map skipped when
// it is empty, as the ACL module skips it.
static inline uint32_t
path_rule(
	const struct yanet_sys_bench_paths *q,
	uint32_t path,
	uint32_t nets,
	uint32_t ports
) {
	uint32_t rule = rule_table_get(q->tables[path], nets, ports);
	const struct vline *map = q->maps[path];
	if (map->size != 0) {
		uint32_t plain = vline_get((struct vline *)map, nets);
		classify_results_min(&rule, &plain, 1);
	}
	return rule;
}

static inline uint32_t
paths_query(
	const struct yanet_sys_bench_paths *q,
	const struct yanet_sys_bench_path_key *pkey
) {
	const struct yanet_sys_bench_key *key = &pkey->key;
	uint32_t src, dst, nets, ports;
	classify_net4_lookup(q->src, &key->src, &src, 1);
	classify_net4_lookup(q->dst, &key->dst, &dst, 1);
	uint32_t sport = vline_get((struct vline *)&q->sport->line, key->sport);
	uint32_t dport = vline_get((struct vline *)&q->dport->line, key->dport);
	classify_joint_lookup(q->nets, &src, &dst, &nets, 1);
	classify_joint_lookup(q->ports, &sport, &dport, &ports, 1);
	return path_rule(q, pkey->path, nets, ports);
}

// Queries each key of the paths once per round, one key per query, and
// returns the sum of the rules.
uint64_t
yanet_sys_bench_c_run_paths(
	const struct yanet_sys_bench_paths *q,
	const struct yanet_sys_bench_path_key *keys,
	uint32_t count,
	uint64_t rounds
) {
	uint64_t sum = 0;
	for (uint64_t round = 0; round < rounds; ++round) {
		__asm__ volatile("" ::: "memory");
		for (uint32_t idx = 0; idx < count; ++idx) {
			sum += paths_query(q, keys + idx);
		}
	}
	return sum;
}

// As yanet_sys_bench_c_run_paths, a burst of 32 keys at a time: the core
// and the ports over the whole burst, then the rule of each key on its path.
uint64_t
yanet_sys_bench_c_run_paths_burst(
	const struct yanet_sys_bench_paths *q,
	const struct yanet_sys_bench_path_key *keys,
	uint32_t count,
	uint64_t rounds
) {
	uint64_t sum = 0;
	for (uint64_t round = 0; round < rounds; ++round) {
		__asm__ volatile("" ::: "memory");
		for (uint32_t start = 0; start < count; start += 32) {
			uint32_t n = count - start < 32 ? count - start : 32;
			const struct yanet_sys_bench_path_key *burst =
				keys + start;
			uint32_t src_addrs[32], dst_addrs[32];
			uint32_t src[32], dst[32], sport[32], dport[32];
			uint32_t nets[32], ports[32];
			for (uint32_t idx = 0; idx < n; ++idx) {
				src_addrs[idx] = burst[idx].key.src;
				dst_addrs[idx] = burst[idx].key.dst;
			}
			classify_net4_lookup(q->src, src_addrs, src, n);
			classify_net4_lookup(q->dst, dst_addrs, dst, n);
			for (uint32_t idx = 0; idx < n; ++idx) {
				sport[idx] = vline_get(
					(struct vline *)&q->sport->line,
					burst[idx].key.sport
				);
				dport[idx] = vline_get(
					(struct vline *)&q->dport->line,
					burst[idx].key.dport
				);
			}
			classify_joint_lookup(q->nets, src, dst, nets, n);
			classify_joint_lookup(q->ports, sport, dport, ports, n);
			for (uint32_t idx = 0; idx < n; ++idx) {
				sum += path_rule(
					q,
					burst[idx].path,
					nets[idx],
					ports[idx]
				);
			}
		}
	}
	return sum;
}
