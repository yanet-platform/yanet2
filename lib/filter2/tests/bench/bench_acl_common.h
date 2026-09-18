// Shared driver of the acl module benchmarks: loads the complete
// ruleset dump (v4 and v6 networks, protocols, ports, devices, vlans,
// fragment) into rule arrays and builds the five module projections.
//
// The dump comes from dump_acl_ruleset.py over an acl module config
// export; every rule is present in it, the projections are built here
// from the parsed rule fields.

#pragma once

// Shared driver for the full acl module benchmarks: loads the complete
// ruleset dump (v4 and v6 networks, protocols, ports, devices, vlans,
// fragment) into rule arrays and builds the five module projections.

#include "common/memory.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

struct bench_cursor {
	const uint8_t *p;
};

static inline uint8_t
bench_rd8(struct bench_cursor *c) {
	return *c->p++;
}

static inline uint16_t
bench_rd16(struct bench_cursor *c) {
	uint16_t v;
	memcpy(&v, c->p, 2);
	c->p += 2;
	return v;
}

static inline uint32_t
bench_rd32(struct bench_cursor *c) {
	uint32_t v;
	memcpy(&v, c->p, 4);
	c->p += 4;
	return v;
}

static inline uint64_t
bench_rd64(struct bench_cursor *c) {
	uint64_t v;
	memcpy(&v, c->p, 8);
	c->p += 8;
	return v;
}

struct bench_stats {
	uint32_t rule_count;
	uint32_t l2_count;
	uint32_t ip4_count;
	uint32_t ip4_port_count;
	uint32_t ip6_count;
	uint32_t ip6_port_count;
};

// Reads the dump twice: once to size the backing arrays, once to fill
// the rule array; every rule of the dump is present, the projections
// are built by the caller from the parsed rules.
static int
bench_load_full(
	const char *path,
	struct filter_rule **rules_out,
	const struct filter_rule ***ptrs_out,
	struct bench_stats *stats
) {
	FILE *f = fopen(path, "rb");
	if (f == NULL) {
		perror("fopen");
		return -1;
	}
	fseek(f, 0, SEEK_END);
	long size = ftell(f);
	fseek(f, 0, SEEK_SET);
	uint8_t *buf = malloc(size);
	if (buf == NULL || fread(buf, 1, size, f) != (size_t)size) {
		return -1;
	}
	fclose(f);

	struct bench_cursor c = {buf};
	uint32_t rule_count = bench_rd32(&c);

	uint64_t v6_count = 0, v4_count = 0, range_count = 0, device_count = 0;
	struct bench_cursor scan = c;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		bench_rd8(&scan);
		for (int side = 0; side < 2; ++side) {
			uint32_t n = bench_rd32(&scan);
			v6_count += n;
			scan.p += 32 * n;
		}
		for (int side = 0; side < 2; ++side) {
			uint32_t n = bench_rd32(&scan);
			v4_count += n;
			scan.p += 8 * n;
		}
		for (uint32_t k = 0; k < 3; ++k) {
			uint32_t n = bench_rd32(&scan);
			range_count += n;
			scan.p += 4 * n;
		}
		scan.p += 1; // fragment kind
		uint32_t devs = bench_rd32(&scan);
		device_count += devs;
		scan.p += 8 * devs;
		uint32_t vlans = bench_rd32(&scan);
		range_count += vlans;
		scan.p += 4 * vlans;
	}

	struct net6 *nets6 =
		malloc(sizeof(struct net6) * (v6_count ? v6_count : 1));
	struct net4 *nets4 =
		malloc(sizeof(struct net4) * (v4_count ? v4_count : 1));
	struct filter_proto_range *protos =
		malloc(sizeof(*protos) * (range_count ? range_count : 1));
	struct filter_port_range *ports =
		malloc(sizeof(*ports) * (range_count ? range_count : 1));
	struct filter_vlan_range *vlans =
		malloc(sizeof(*vlans) * (range_count ? range_count : 1));
	struct filter_device *devices =
		malloc(sizeof(*devices) * (device_count ? device_count : 1));
	struct filter_rule *rules = calloc(rule_count, sizeof(*rules));
	const struct filter_rule **ptrs = malloc(sizeof(*ptrs) * rule_count);
	if (!nets6 || !nets4 || !protos || !ports || !vlans || !devices ||
	    !rules || !ptrs) {
		return -1;
	}

	uint64_t n6 = 0, n4 = 0, proto_pos = 0, port_pos = 0, vlan_pos = 0,
		 dev_pos = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct filter_rule *rule = rules + idx;
		ptrs[idx] = rule;
		bench_rd8(&c); // presence, always set in the full dump

		for (int side = 0; side < 2; ++side) {
			uint32_t count = bench_rd32(&c);
			struct net6 *dst6 = nets6 + n6;
			n6 += count;
			for (uint32_t k = 0; k < count; ++k) {
				memcpy(dst6[k].addr, c.p, 16);
				c.p += 16;
				memcpy(dst6[k].mask, c.p, 16);
				c.p += 16;
			}
			if (side == 0) {
				rule->net6.src_count = count;
				rule->net6.srcs = dst6;
			} else {
				rule->net6.dst_count = count;
				rule->net6.dsts = dst6;
			}
		}

		for (int side = 0; side < 2; ++side) {
			uint32_t count = bench_rd32(&c);
			struct net4 *dst4 = nets4 + n4;
			n4 += count;
			for (uint32_t k = 0; k < count; ++k) {
				memcpy(dst4[k].addr, c.p, 4);
				c.p += 4;
				memcpy(dst4[k].mask, c.p, 4);
				c.p += 4;
			}
			if (side == 0) {
				rule->net4.src_count = count;
				rule->net4.srcs = dst4;
			} else {
				rule->net4.dst_count = count;
				rule->net4.dsts = dst4;
			}
		}

		uint32_t pc = bench_rd32(&c);
		rule->transport.proto_count = pc;
		rule->transport.protos = protos + proto_pos;
		for (uint32_t k = 0; k < pc; ++k) {
			protos[proto_pos].from = bench_rd16(&c);
			protos[proto_pos].to = bench_rd16(&c);
			++proto_pos;
		}

		for (int side = 0; side < 2; ++side) {
			uint32_t count = bench_rd32(&c);
			struct filter_port_range *dstp = ports + port_pos;
			port_pos += count;
			for (uint32_t k = 0; k < count; ++k) {
				dstp[k].from = bench_rd16(&c);
				dstp[k].to = bench_rd16(&c);
			}
			if (side == 0) {
				rule->transport.src_count = count;
				rule->transport.srcs = dstp;
			} else {
				rule->transport.dst_count = count;
				rule->transport.dsts = dstp;
			}
		}

		rule->fragment = bench_rd8(&c);

		uint32_t dc = bench_rd32(&c);
		rule->device_count = dc;
		rule->devices = devices + dev_pos;
		for (uint32_t k = 0; k < dc; ++k) {
			memset(&devices[dev_pos], 0, sizeof(devices[dev_pos]));
			devices[dev_pos].id = bench_rd64(&c);
			++dev_pos;
		}

		uint32_t vc = bench_rd32(&c);
		rule->vlan_range_count = vc;
		rule->vlan_ranges = vlans + vlan_pos;
		for (uint32_t k = 0; k < vc; ++k) {
			vlans[vlan_pos].from = bench_rd16(&c);
			vlans[vlan_pos].to = bench_rd16(&c);
			++vlan_pos;
		}
	}

	stats->rule_count = rule_count;
	*rules_out = rules;
	*ptrs_out = ptrs;
	return 0;
}

// Port window semantics of the acl module projections, fragment aware.
static int
bench_full_ports(const struct filter_rule *rule) {
	int full_frag = rule->fragment == 0 || rule->fragment == 1;
	int frag_only = rule->fragment == 2;
	int full_src = rule->transport.src_count == 0 ||
		       (rule->transport.srcs[0].from == 0 &&
			rule->transport.srcs[0].to == 65535);
	int full_dst = rule->transport.dst_count == 0 ||
		       (rule->transport.dsts[0].from == 0 &&
			rule->transport.dsts[0].to == 65535);
	return ((full_frag && full_src && full_dst) || frag_only);
}

static int
bench_has_ip4(const struct filter_rule *rule) {
	return rule->net4.src_count && rule->net4.dst_count;
}

static int
bench_has_ip6(const struct filter_rule *rule) {
	return rule->net6.src_count && rule->net6.dst_count;
}

// Builds a projection pointer array over the rules following one of
// the acl module checks; returns the present count.
static uint32_t
bench_project(
	const struct filter_rule **dst,
	const struct filter_rule **src,
	uint32_t rule_count,
	int (*check)(const struct filter_rule *)
) {
	uint32_t present = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		if (check(src[idx])) {
			dst[idx] = src[idx];
			++present;
		} else {
			dst[idx] = NULL;
		}
	}
	return present;
}

static int
check_l2(const struct filter_rule *rule) {
	return !rule->net6.src_count && !rule->net6.dst_count &&
	       !rule->net4.src_count && !rule->net4.dst_count;
}

static int
check_ip4(const struct filter_rule *rule) {
	return bench_has_ip4(rule) && bench_full_ports(rule);
}

static int
check_ip4_port(const struct filter_rule *rule) {
	return bench_has_ip4(rule) && !bench_full_ports(rule);
}

static int
check_ip6(const struct filter_rule *rule) {
	return bench_has_ip6(rule) && bench_full_ports(rule);
}

static int
check_ip6_port(const struct filter_rule *rule) {
	return bench_has_ip6(rule) && !bench_full_ports(rule);
}

static int
check_has4_any(const struct filter_rule *rule) {
	return bench_has_ip4(rule);
}

static int
check_has6_any(const struct filter_rule *rule) {
	return bench_has_ip6(rule);
}

static inline double
bench_ms(const struct timespec *t0, const struct timespec *t1) {
	return (t1->tv_sec - t0->tv_sec) * 1e3 +
	       (t1->tv_nsec - t0->tv_nsec) / 1e6;
}
