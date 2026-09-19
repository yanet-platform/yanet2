// acl module benchmark, classifier composition path: the vlan filter
// plus a v4 and a v6 network classifier each joined with the
// projections of the ip and the ip port signatures, then the v6 lookup
// path (both v6 filters per packet, first match merge) over a capture.
//
// Usage: bench_acl_core <rules.bin> <capture.pcap> [arena MiB]
//                   [results.bin]
//
// The ruleset dump comes from dump_acl_ruleset.py; with results.bin on
// both benchmarks the per packet rule indices must match the production
// path byte for byte.

#include "lib/classify/classify.h"
#include "lib/classify/compiler.h"
#include "lib/classify/query.h"

#include "modules/acl/dataplane/filter_lookup.h"

#include "bench_acl_common.h"
#include "bench_pcap.h"

static const struct classify_attr_handlers *sign_vlan[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
};

static const struct classify_attr_handlers *sign_v4_full[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net4_src),
	CLASSIFY_ATTR(net4_dst),
	CLASSIFY_ATTR(ipfrag),
	CLASSIFY_ATTR(proto_range),
};

static const struct classify_attr_handlers *sign_v6_full[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net6_src),
	CLASSIFY_ATTR(net6_dst),
	CLASSIFY_ATTR(ipfrag),
	CLASSIFY_ATTR(proto_range),
};

static const struct classify_attr_handlers *sign_ports[] = {
	CLASSIFY_ATTR(port_src),
	CLASSIFY_ATTR(port_dst),
};

#define BATCH 64

static uint32_t
merge_first(uint32_t a, uint32_t b) {
	if (a == CLASSIFY_RULE_INVALID) {
		return b;
	}
	if (b == CLASSIFY_RULE_INVALID) {
		return a;
	}
	return a < b ? a : b;
}

// Builds the ip family classifier over the union of both family
// projections and freezes the plain family filter from it, decoded
// over the plain projection. Mirrors acl_module_build_ip_classifier.
static int
build_ip_filter(
	struct classify_filter *filter,
	struct memory_context *mctx,
	const struct classify_attr_handlers *full_sign[],
	uint32_t full_count,
	const struct filter_rule **union_proj,
	const struct filter_rule **plain_proj,
	uint32_t rule_count,
	struct classifier **out_cls
) {
	struct classifier *cls = classify_build(
		mctx, full_sign, full_count, union_proj, rule_count
	);
	if (cls == NULL) {
		return -1;
	}

	struct vline *decoder = classify_decode(cls, mctx, plain_proj);
	if (decoder == NULL) {
		classify_free(cls);
		return -1;
	}

	if (classify_filter_init(filter, mctx, cls, decoder)) {
		classify_free(cls);
		return -1;
	}

	*out_cls = cls;
	return 0;
}

// Builds the port scoped filter: a ports classifier over the port
// scoped projection joined with the family classifier, decoded over
// the same projection. Mirrors acl_module_build_port_filter.
static int
build_port_filter(
	struct classify_filter *filter,
	struct memory_context *mctx,
	struct classifier *ip_cls,
	const struct filter_rule **port_proj,
	uint32_t rule_count,
	struct classifier **out_cls
) {
	struct classifier *ports =
		classify_build(mctx, sign_ports, 2, port_proj, rule_count);
	if (ports == NULL) {
		return -1;
	}

	struct classifier *cls = classify_join(mctx, ip_cls, ports);
	classify_free(ports);
	if (cls == NULL) {
		return -1;
	}

	struct vline *decoder = classify_decode(cls, mctx, port_proj);
	if (decoder == NULL) {
		classify_free(cls);
		return -1;
	}

	if (classify_filter_init(filter, mctx, cls, decoder)) {
		classify_free(cls);
		return -1;
	}

	*out_cls = cls;
	return 0;
}

int
main(int argc, char **argv) {
	if (argc < 3) {
		fprintf(stderr,
			"usage: %s <rules.bin> <capture.pcap> [arena MiB] "
			"[results.bin]\n",
			argv[0]);
		return 1;
	}
	const char *rule_path = argv[1];
	const char *pcap_path = argv[2];
	size_t arena_mb = argc > 3 ? (size_t)atol(argv[3]) : 40000;
	const char *result_path = argc > 4 ? argv[4] : NULL;

	(void)acl_query_vlan;
	(void)acl_query_ip4;
	(void)acl_query_ip4_port;

	struct filter_rule *rules;
	const struct filter_rule **all;
	struct bench_stats stats;
	if (bench_load_full(rule_path, &rules, &all, &stats)) {
		return 1;
	}

	const struct filter_rule **proj =
		malloc(sizeof(*proj) * stats.rule_count * 6);
	uint32_t n_l2 = bench_project(proj, all, stats.rule_count, check_l2);
	uint32_t n_ip4 = bench_project(
		proj + stats.rule_count, all, stats.rule_count, check_ip4
	);
	uint32_t n_ip4p = bench_project(
		proj + 2 * stats.rule_count,
		all,
		stats.rule_count,
		check_ip4_port
	);
	uint32_t n_ip6 = bench_project(
		proj + 3 * stats.rule_count, all, stats.rule_count, check_ip6
	);
	uint32_t n_ip6p = bench_project(
		proj + 4 * stats.rule_count,
		all,
		stats.rule_count,
		check_ip6_port
	);
	printf("new acl.in: rules=%u l2=%u ip4=%u ip4_port=%u ip6=%u "
	       "ip6_port=%u\n",
	       stats.rule_count,
	       n_l2,
	       n_ip4,
	       n_ip4p,
	       n_ip6,
	       n_ip6p);

	const struct filter_rule **v4_union =
		malloc(sizeof(*v4_union) * stats.rule_count);
	const struct filter_rule **v6_union =
		malloc(sizeof(*v6_union) * stats.rule_count);
	bench_project(v4_union, all, stats.rule_count, check_has4_any);
	bench_project(v6_union, all, stats.rule_count, check_has6_any);

	void *arena = malloc(arena_mb << 20);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_mb << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "bench", &allocator);

	struct timespec t0, t1, tall0, tall1;
	int rc = 0;

	struct classify_filter flt_vlan;
	struct classifier *cls_vlan = NULL;
	struct vline *dec_vlan = NULL;
	struct classifier *cls_ip4 = NULL, *cls_ip4p = NULL;
	struct classify_filter flt_ip4, flt_ip4p;
	struct classifier *cls_ip6 = NULL, *cls_ip6p = NULL;
	struct classify_filter flt_ip6, flt_ip6p;
	memset(&flt_vlan, 0, sizeof(flt_vlan));
	memset(&flt_ip4, 0, sizeof(flt_ip4));
	memset(&flt_ip4p, 0, sizeof(flt_ip4p));
	memset(&flt_ip6, 0, sizeof(flt_ip6));
	memset(&flt_ip6p, 0, sizeof(flt_ip6p));

	clock_gettime(CLOCK_MONOTONIC, &tall0);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	cls_vlan = classify_build(&mctx, sign_vlan, 2, proj, stats.rule_count);
	dec_vlan = cls_vlan ? classify_decode(cls_vlan, &mctx, proj) : NULL;
	rc |= cls_vlan == NULL || dec_vlan == NULL ||
	      classify_filter_init(&flt_vlan, &mctx, cls_vlan, dec_vlan);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new vlan:      %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_ip_filter(
		&flt_ip4,
		&mctx,
		sign_v4_full,
		6,
		v4_union,
		proj + stats.rule_count,
		stats.rule_count,
		&cls_ip4
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_port_filter(
		&flt_ip4p,
		&mctx,
		cls_ip4,
		proj + 2 * stats.rule_count,
		stats.rule_count,
		&cls_ip4p
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_ip_filter(
		&flt_ip6,
		&mctx,
		sign_v6_full,
		6,
		v6_union,
		proj + 3 * stats.rule_count,
		stats.rule_count,
		&cls_ip6
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_port_filter(
		&flt_ip6p,
		&mctx,
		cls_ip6,
		proj + 4 * stats.rule_count,
		stats.rule_count,
		&cls_ip6p
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &tall1);
	printf("new compile total: %.1f ms rc=%d\n",
	       bench_ms(&tall0, &tall1),
	       rc);
	if (rc) {
		return 1;
	}

	struct bench_capture cap;
	if (bench_pcap_load(pcap_path, &cap)) {
		return 1;
	}

	uint32_t *r6 = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *r6p = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *merged = malloc(sizeof(uint32_t) * cap.count);

	// The batch partitioning follows the production dataplane: the ip6
	// filters see IPv6 packets only, the port scoped filter sees offset
	// zero TCP or UDP among them.
	struct packet **ip6_packets = malloc(sizeof(*ip6_packets) * cap.count);
	struct packet **ip6_port_packets =
		malloc(sizeof(*ip6_port_packets) * cap.count);
	uint32_t *ip6_pos = malloc(sizeof(*ip6_pos) * cap.count);
	uint32_t *ip6_port_pos = malloc(sizeof(*ip6_port_pos) * cap.count);
	uint32_t ip6_count = 0;
	uint32_t ip6_port_count = 0;
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		if (!bench_packet_is_ip6(cap.packets + idx)) {
			continue;
		}
		ip6_pos[ip6_count] = idx;
		ip6_packets[ip6_count++] = cap.ptrs[idx];
		if (bench_packet_is_ip6_port(cap.packets + idx)) {
			ip6_port_pos[ip6_port_count] = idx;
			ip6_port_packets[ip6_port_count++] = cap.ptrs[idx];
		}
	}
	printf("capture: ip6 %u/%u, port scoped %u\n",
	       ip6_count,
	       cap.count,
	       ip6_port_count);

	for (uint32_t off = 0; off < ip6_count; off += BATCH) {
		uint32_t n = ip6_count - off < BATCH ? ip6_count - off : BATCH;
		classify_query(
			&flt_ip6, acl_query_ip6, ip6_packets + off, r6 + off, n
		);
	}
	for (uint32_t off = 0; off < ip6_port_count; off += BATCH) {
		uint32_t n = ip6_port_count - off < BATCH ? ip6_port_count - off
							  : BATCH;
		classify_query(
			&flt_ip6p,
			acl_query_ip6_port,
			ip6_port_packets + off,
			r6p + off,
			n
		);
	}

	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		merged[idx] = FILTER_RULE_INVALID;
	}
	uint32_t matched = 0;
	for (uint32_t idx = 0; idx < ip6_count; ++idx) {
		merged[ip6_pos[idx]] = r6[idx];
	}
	for (uint32_t idx = 0; idx < ip6_port_count; ++idx) {
		uint32_t pos = ip6_port_pos[idx];
		merged[pos] = merge_first(merged[pos], r6p[idx]);
	}
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		matched += merged[idx] != FILTER_RULE_INVALID;
	}
	printf("new v6 lookup: matched %u/%u (%.1f%%) fnv=%llx\n",
	       matched,
	       cap.count,
	       100.0 * matched / cap.count,
	       (unsigned long long)bench_fnv1a(merged, cap.count));
	if (result_path != NULL) {
		FILE *rf = fopen(result_path, "wb");
		fwrite(merged, sizeof(uint32_t), cap.count, rf);
		fclose(rf);
	}

	const uint32_t passes = 50;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	for (uint32_t pass = 0; pass < passes; ++pass) {
		for (uint32_t off = 0; off < ip6_count; off += BATCH) {
			uint32_t n = ip6_count - off < BATCH ? ip6_count - off
							     : BATCH;
			classify_query(
				&flt_ip6,
				acl_query_ip6,
				ip6_packets + off,
				r6 + off,
				n
			);
		}
		for (uint32_t off = 0; off < ip6_port_count; off += BATCH) {
			uint32_t n = ip6_port_count - off < BATCH
					     ? ip6_port_count - off
					     : BATCH;
			classify_query(
				&flt_ip6p,
				acl_query_ip6_port,
				ip6_port_packets + off,
				r6p + off,
				n
			);
		}
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v6 steady: %.1f ns/pkt (both filters)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

	free(ip6_packets);
	free(ip6_port_packets);
	free(ip6_pos);
	free(ip6_port_pos);

	classify_filter_free(&flt_ip6p);
	classify_free(cls_ip6p);
	classify_filter_free(&flt_ip6);
	classify_free(cls_ip6);
	classify_filter_free(&flt_ip4p);
	classify_free(cls_ip4p);
	classify_filter_free(&flt_ip4);
	classify_free(cls_ip4);
	classify_filter_free(&flt_vlan);
	classify_free(cls_vlan);
	bench_capture_free(&cap);
	return 0;
}
