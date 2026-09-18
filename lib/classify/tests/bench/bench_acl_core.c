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

#include "bench_acl_common.h"
#include "bench_pcap.h"

static const struct classify_attr_handlers *sign_vlan[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
};

static const struct classify_attr_handlers *sign_v4_core[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net4_src),
	CLASSIFY_ATTR(net4_dst),
};

static const struct classify_attr_handlers *sign_v4_ip4[] = {
	CLASSIFY_ATTR(ipfrag),
	CLASSIFY_ATTR(proto_range),
};

static const struct classify_attr_handlers *sign_v4_ip4_port[] = {
	CLASSIFY_ATTR(proto_range),
	CLASSIFY_ATTR(port_src),
	CLASSIFY_ATTR(port_dst),
};

static const struct classify_attr_handlers *sign_v6_core[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net6_src),
	CLASSIFY_ATTR(net6_dst),
};

static const struct classify_attr_handlers *sign_v6_ip6[] = {
	CLASSIFY_ATTR(ipfrag),
	CLASSIFY_ATTR(proto_range),
};

static const struct classify_attr_handlers *sign_v6_ip6_port[] = {
	CLASSIFY_ATTR(proto_range),
	CLASSIFY_ATTR(port_src),
	CLASSIFY_ATTR(port_dst),
};

CLASSIFY_QUERY_DECLARE(
	q_ip6, device, vlan, net6_src, net6_dst, ipfrag, proto_range
);
CLASSIFY_QUERY_DECLARE(
	q_ip6_port,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);

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

// Builds one filter of the composition: a classifier over the tail
// signature joined onto the shared core, decoded over the projection
// and frozen into a filter. The join consumes the tail classifier; it
// is freed with the joined root.
static int
compose_filter(
	struct classify_filter *filter,
	struct memory_context *mctx,
	struct classifier *core,
	const struct classify_attr_handlers *tail_sign[],
	uint32_t tail_count,
	const struct filter_rule **projection,
	uint32_t rule_count,
	struct classifier **out_cls,
	struct vline **out_decoder
) {
	struct classifier *tail = classify_build(
		mctx, tail_sign, tail_count, projection, rule_count
	);
	if (tail == NULL) {
		return -1;
	}

	struct classifier *cls = classify_join(mctx, core, tail);
	classify_free(tail);
	if (cls == NULL) {
		return -1;
	}

	struct vline *decoder = classify_decode(cls, mctx, projection);
	if (decoder == NULL) {
		classify_free(cls);
		return -1;
	}

	if (classify_filter_init(filter, mctx, cls, decoder)) {
		classify_free(cls);
		return -1;
	}

	*out_cls = cls;
	*out_decoder = decoder;
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
	struct classifier *core4 = NULL, *cls_ip4 = NULL, *cls_ip4p = NULL;
	struct classify_filter flt_ip4, flt_ip4p;
	struct vline *dec_ip4 = NULL, *dec_ip4p = NULL;
	struct classifier *core6 = NULL, *cls_ip6 = NULL, *cls_ip6p = NULL;
	struct classify_filter flt_ip6, flt_ip6p;
	struct vline *dec_ip6 = NULL, *dec_ip6p = NULL;
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
	core4 = classify_build(
		&mctx, sign_v4_core, 4, v4_union, stats.rule_count
	);
	rc |= core4 == NULL;
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v4 core:   %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= compose_filter(
		&flt_ip4,
		&mctx,
		core4,
		sign_v4_ip4,
		2,
		proj + stats.rule_count,
		stats.rule_count,
		&cls_ip4,
		&dec_ip4
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= compose_filter(
		&flt_ip4p,
		&mctx,
		core4,
		sign_v4_ip4_port,
		3,
		proj + 2 * stats.rule_count,
		stats.rule_count,
		&cls_ip4p,
		&dec_ip4p
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	core6 = classify_build(
		&mctx, sign_v6_core, 4, v6_union, stats.rule_count
	);
	rc |= core6 == NULL;
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v6 core:   %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= compose_filter(
		&flt_ip6,
		&mctx,
		core6,
		sign_v6_ip6,
		2,
		proj + 3 * stats.rule_count,
		stats.rule_count,
		&cls_ip6,
		&dec_ip6
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= compose_filter(
		&flt_ip6p,
		&mctx,
		core6,
		sign_v6_ip6_port,
		3,
		proj + 4 * stats.rule_count,
		stats.rule_count,
		&cls_ip6p,
		&dec_ip6p
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

	for (uint32_t off = 0; off < cap.count; off += BATCH) {
		uint32_t n = cap.count - off < BATCH ? cap.count - off : BATCH;
		classify_query(&flt_ip6, q_ip6, cap.ptrs + off, r6 + off, n);
		classify_query(
			&flt_ip6p, q_ip6_port, cap.ptrs + off, r6p + off, n
		);
	}
	uint32_t matched = 0;
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		merged[idx] = merge_first(r6[idx], r6p[idx]);
		matched += merged[idx] != CLASSIFY_RULE_INVALID;
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
		for (uint32_t off = 0; off < cap.count; off += BATCH) {
			uint32_t n = cap.count - off < BATCH ? cap.count - off
							     : BATCH;
			classify_query(
				&flt_ip6, q_ip6, cap.ptrs + off, r6 + off, n
			);
			classify_query(
				&flt_ip6p,
				q_ip6_port,
				cap.ptrs + off,
				r6p + off,
				n
			);
		}
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v6 steady: %.1f ns/pkt (both filters)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

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
	classify_free(core6);
	classify_free(core4);
	bench_capture_free(&cap);
	return 0;
}
