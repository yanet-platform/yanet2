// forward module benchmark, classify library path: the three classifier
// builds of the ported forward module over a dumped ruleset, then the
// lookup path (vlan plus the family filter per packet, first match
// merge) over a capture.
//
// Usage: bench_forward_classify <rules.bin> <capture.pcap> [arena MiB]
//                           [results.bin]
//
// The ruleset dump comes from dump_acl_ruleset.py; with results.bin on
// both benchmarks the per packet rule indices must match the production
// path byte for byte.

#include "lib/classify/classify.h"
#include "lib/classify/compiler.h"
#include "lib/classify/query.h"

#include "bench_acl_common.h"
#include "bench_pcap.h"

static const struct classify_attr_handlers *sign_fwd_vlan[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
};

static const struct classify_attr_handlers *sign_fwd_ip4[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net4_src),
	CLASSIFY_ATTR(net4_dst),
};

static const struct classify_attr_handlers *sign_fwd_ip6[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net6_src),
	CLASSIFY_ATTR(net6_dst),
};

CLASSIFY_QUERY_DECLARE(q_fwd_vlan, device, vlan);
CLASSIFY_QUERY_DECLARE(q_fwd_ip6, device, vlan, net6_src, net6_dst);

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

// Builds the classifier of a filter signature over the rule projection,
// decodes it and freezes both into the filter; the classifier tree is
// released after the filter at teardown.
static int
build_filter(
	struct classify_filter *filter,
	struct classifier **classifier,
	const struct classify_attr_handlers *sign[],
	uint32_t sign_count,
	const struct filter_rule **projection,
	uint32_t rule_count,
	struct memory_context *mctx
) {
	struct classifier *cls =
		classify_build(mctx, sign, sign_count, projection, rule_count);
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

	*classifier = cls;
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
		malloc(sizeof(*proj) * stats.rule_count * 3);
	uint32_t n_l2 = bench_project(proj, all, stats.rule_count, check_l2);
	uint32_t n_ip4 = bench_project(
		proj + stats.rule_count, all, stats.rule_count, check_has4_any
	);
	uint32_t n_ip6 = bench_project(
		proj + 2 * stats.rule_count,
		all,
		stats.rule_count,
		check_has6_any
	);
	printf("new forward acl.in: rules=%u l2=%u ip4=%u ip6=%u\n",
	       stats.rule_count,
	       n_l2,
	       n_ip4,
	       n_ip6);

	void *arena = malloc(arena_mb << 20);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_mb << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "bench", &allocator);

	struct classify_filter flt_vlan, flt_ip4, flt_ip6;
	struct classifier *cls_vlan = NULL, *cls_ip4 = NULL, *cls_ip6 = NULL;
	memset(&flt_vlan, 0, sizeof(flt_vlan));
	memset(&flt_ip4, 0, sizeof(flt_ip4));
	memset(&flt_ip6, 0, sizeof(flt_ip6));

	struct timespec t0, t1, tall0, tall1;
	int rc = 0;

	clock_gettime(CLOCK_MONOTONIC, &tall0);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_filter(
		&flt_vlan,
		&cls_vlan,
		sign_fwd_vlan,
		2,
		proj,
		stats.rule_count,
		&mctx
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new vlan: %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_filter(
		&flt_ip4,
		&cls_ip4,
		sign_fwd_ip4,
		4,
		proj + stats.rule_count,
		stats.rule_count,
		&mctx
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4:  %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= build_filter(
		&flt_ip6,
		&cls_ip6,
		sign_fwd_ip6,
		4,
		proj + 2 * stats.rule_count,
		stats.rule_count,
		&mctx
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6:  %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
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

	uint32_t *rv = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *rf = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *merged = malloc(sizeof(uint32_t) * cap.count);

	for (uint32_t off = 0; off < cap.count; off += BATCH) {
		uint32_t n = cap.count - off < BATCH ? cap.count - off : BATCH;
		classify_query(
			&flt_vlan, q_fwd_vlan, cap.ptrs + off, rv + off, n
		);
		classify_query(
			&flt_ip6, q_fwd_ip6, cap.ptrs + off, rf + off, n
		);
	}
	uint32_t matched = 0;
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		merged[idx] = merge_first(rv[idx], rf[idx]);
		matched += merged[idx] != CLASSIFY_RULE_INVALID;
	}
	printf("new v6 lookup: matched %u/%u (%.1f%%) fnv=%llx\n",
	       matched,
	       cap.count,
	       100.0 * matched / cap.count,
	       (unsigned long long)bench_fnv1a(merged, cap.count));
	if (result_path != NULL) {
		FILE *rfp = fopen(result_path, "wb");
		fwrite(merged, sizeof(uint32_t), cap.count, rfp);
		fclose(rfp);
	}

	const uint32_t passes = 50;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	for (uint32_t pass = 0; pass < passes; ++pass) {
		for (uint32_t off = 0; off < cap.count; off += BATCH) {
			uint32_t n = cap.count - off < BATCH ? cap.count - off
							     : BATCH;
			classify_query(
				&flt_vlan,
				q_fwd_vlan,
				cap.ptrs + off,
				rv + off,
				n
			);
			classify_query(
				&flt_ip6, q_fwd_ip6, cap.ptrs + off, rf + off, n
			);
		}
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v6 steady: %.1f ns/pkt (vlan plus ip6)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

	classify_filter_free(&flt_ip6);
	classify_free(cls_ip6);
	classify_filter_free(&flt_ip4);
	classify_free(cls_ip4);
	classify_filter_free(&flt_vlan);
	classify_free(cls_vlan);
	(void)check_ip4;
	(void)check_ip4_port;
	(void)check_ip6;
	(void)check_ip6_port;
	bench_capture_free(&cap);
	return 0;
}
