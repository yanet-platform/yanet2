// The inline C query the classify benchmark of the SDK measures the Rust ones
// against: the per-key query a C module composes from the header-inline
// lookups of lib/classify, compiled by the dataplane's compiler and flags.
#include <stdint.h>

#include "common/value.h"

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/query.h"

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
