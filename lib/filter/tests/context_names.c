// Pins the leaf and inner memory_context naming: nodes are named after the
// attribute (or attribute pair) they classify instead of their heap-index
// position, and an attribute's own payload nests under its own leaf instead
// of colliding with a sibling attribute on a shared name.
#include "common/asan.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"

#include "lib/filter/tests/helpers.h"

#include "logging/log.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_small_compile, device, vlan, port_src, port_dst);
FILTER_COMPILER_DECLARE(
	sign_large_compile, device, vlan, port_src, port_dst, proto_range
);
FILTER_COMPILER_DECLARE(sign_net6_compile, net6_src, net6_dst);

#define CTX_NAMES_ARENA_SIZE (1 << 24)

static struct memory_context *
ctx_child(struct memory_context *ctx) {
	return ADDR_OF(&ctx->first_child);
}

static struct memory_context *
ctx_sibling(struct memory_context *ctx) {
	return ADDR_OF(&ctx->next_sibling);
}

// Counts the direct children of ctx named name.
static int
direct_child_count(struct memory_context *ctx, const char *name) {
	int count = 0;
	for (struct memory_context *child = ctx_child(ctx); child != NULL;
	     child = ctx_sibling(child)) {
		if (strcmp(child->name, name) == 0) {
			++count;
		}
	}
	return count;
}

// Recursively checks that no two siblings, at any depth under ctx, share a
// name.
static int
no_duplicate_siblings(struct memory_context *ctx) {
	for (struct memory_context *child = ctx_child(ctx); child != NULL;
	     child = ctx_sibling(child)) {
		for (struct memory_context *sibling = ctx_sibling(child);
		     sibling != NULL;
		     sibling = ctx_sibling(sibling)) {
			TEST_ASSERT(
				strcmp(child->name, sibling->name) != 0,
				"duplicate sibling name '%s' under '%s'",
				child->name,
				ctx->name
			);
		}
		TEST_ASSERT_SUCCESS(
			no_duplicate_siblings(child),
			"duplicate siblings under '%s'",
			child->name
		);
	}
	return TEST_SUCCESS;
}

// Recursively checks that no context name under ctx contains '[' or ']'.
static int
no_index_syntax(struct memory_context *ctx) {
	for (struct memory_context *child = ctx_child(ctx); child != NULL;
	     child = ctx_sibling(child)) {
		TEST_ASSERT(
			strchr(child->name, '[') == NULL &&
				strchr(child->name, ']') == NULL,
			"index-shaped name '%s'",
			child->name
		);
		TEST_ASSERT_SUCCESS(
			no_index_syntax(child), "under '%s'", child->name
		);
	}
	return TEST_SUCCESS;
}

static int
build_filter(
	const struct filter_compiler *compiler,
	struct memory_context *mctx,
	const char *name,
	struct filter *filter
) {
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_port_src_range(&b1, 5, 7);
	builder_add_port_dst_range(&b1, 1, 5);
	builder_set_vlan(&b1, 10);
	builder_add_proto_range(&b1, 6, 6);
	struct filter_rule r1 = build_rule(&b1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_port_src_range(&b2, 8, 9);
	builder_add_port_dst_range(&b2, 6, 9);
	builder_set_vlan(&b2, 20);
	builder_add_proto_range(&b2, 17, 17);
	struct filter_rule r2 = build_rule(&b2);

	const struct filter_rule *rule_ptrs[2] = {&r1, &r2};

	return filter_init(filter, compiler, rule_ptrs, 2, mctx, name, NULL);
}

static struct net6
net6_cidr(const uint8_t addr[NET6_LEN], int prefix_len) {
	struct net6 net;
	memcpy(net.addr, addr, NET6_LEN);
	memset(net.mask, 0, NET6_LEN);
	for (int idx = 0; idx < prefix_len; ++idx) {
		net.mask[idx / 8] |= 0x80 >> (idx % 8);
	}
	return net;
}

// Builds a net6_src/net6_dst filter from real IPv6 prefixes, driving the
// hi/lo lpm split inside init_net6 that collect_net6_range feeds.
static int
build_net6_filter(
	struct memory_context *mctx, const char *name, struct filter *filter
) {
	const uint8_t dst_prefix[NET6_LEN] = {0xfe, 0x80};
	const uint8_t src_prefix[NET6_LEN] = {0x2a, 0x02, 0x06, 0xb8};

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_net6_dst(&b1, net6_cidr(dst_prefix, 64));
	builder_add_net6_src(&b1, net6_cidr(src_prefix, 32));
	struct filter_rule r1 = build_rule(&b1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_net6_dst(&b2, net6_cidr(dst_prefix, 128));
	builder_add_net6_src(&b2, net6_cidr(src_prefix, 48));
	struct filter_rule r2 = build_rule(&b2);

	const struct filter_rule *rule_ptrs[2] = {&r1, &r2};

	return filter_init(
		filter, sign_net6_compile, rule_ptrs, 2, mctx, name, NULL
	);
}

// Shared per-case fixture: a root memory_context over a fresh block
// allocator, plus the allocator's free-byte count captured before any
// filter is built, so fixture_check_balance can assert that filter_free
// hands back every byte filter_init took.
struct ctx_names_fixture {
	struct block_allocator allocator;
	struct memory_context mctx;
	size_t free_before;
};

static int
fixture_init(struct ctx_names_fixture *fx, void *arena) {
	block_allocator_init(&fx->allocator);
	block_allocator_put_arena(&fx->allocator, arena, CTX_NAMES_ARENA_SIZE);
	if (memory_context_init(&fx->mctx, "test", &fx->allocator)) {
		return -1;
	}
	fx->free_before = block_allocator_free_size(&fx->allocator);
	return 0;
}

static int
fixture_check_balance(struct ctx_names_fixture *fx) {
	TEST_ASSERT_EQUAL(
		block_allocator_free_size(&fx->allocator),
		fx->free_before,
		"arena free size mismatch after filter_free"
	);
	return TEST_SUCCESS;
}

static void
fixture_fini(struct ctx_names_fixture *fx) {
	memory_context_fini(&fx->mctx);
}

// Verifies that every attribute in a signature appears exactly once as a
// direct child of the filter's memory_context, spelled like its
// FILTER_COMPILER_DECLARE literal.
static int
test_leaf_named_after_attribute(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_filter(sign_small_compile, &fx.mctx, "filter", &filter),
		"failed to build small filter"
	);

	static const char *names[] = {"device", "vlan", "port_src", "port_dst"};
	for (size_t i = 0; i < sizeof(names) / sizeof(names[0]); ++i) {
		TEST_ASSERT_EQUAL(
			direct_child_count(&filter.memory_context, names[i]),
			1,
			"attribute '%s' is not a direct child exactly once",
			names[i]
		);
	}

	filter_free(&filter, sign_small_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the small filter"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that a leaf's attribute name is unaffected by appending another
// attribute to the signature, unlike the old heap-index naming.
static int
test_leaf_names_survive_signature_change(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter small_filter;
	TEST_ASSERT_SUCCESS(
		build_filter(
			sign_small_compile,
			&fx.mctx,
			"small_filter",
			&small_filter
		),
		"failed to build small filter"
	);

	struct filter large_filter;
	TEST_ASSERT_SUCCESS(
		build_filter(
			sign_large_compile,
			&fx.mctx,
			"large_filter",
			&large_filter
		),
		"failed to build large filter"
	);

	// Anchor each shared attribute name at exactly one direct child in
	// both trees, rather than only comparing counts against each other:
	// a count-to-count comparison passes 0 == 0 just as well as 1 == 1,
	// so it cannot fail if a rename moves the leaf out from under both
	// trees at once. The tree's direct children also include inner
	// merge nodes, whose names are expected to change shape between a
	// 4- and 5-leaf tree, but the leaves shared by both signatures must
	// not.
	for (uint64_t i = 0; i < sign_small_compile->lookup_count; ++i) {
		const char *name = sign_small_compile->lookups[i].name;
		TEST_ASSERT_EQUAL(
			direct_child_count(&small_filter.memory_context, name),
			1,
			"leaf '%s' is not a direct child of the small filter",
			name
		);
		TEST_ASSERT_EQUAL(
			direct_child_count(&large_filter.memory_context, name),
			1,
			"leaf '%s' did not survive the signature change",
			name
		);
	}

	filter_free(&large_filter, sign_large_compile);
	filter_free(&small_filter, sign_small_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the two filters"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that reparenting attribute payloads under their own leaf ends
// the sibling collision where port_src and port_dst both produced a "port"
// child of the shared filter root.
static int
check_no_sibling_collision(
	const struct filter_compiler *compiler, void *arena, const char *label
) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena),
		"failed to init root memory context for %s signature",
		label
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_filter(compiler, &fx.mctx, "filter", &filter),
		"failed to build %s filter",
		label
	);

	TEST_ASSERT_SUCCESS(
		no_duplicate_siblings(&filter.memory_context),
		"sibling name collision under the %s filter",
		label
	);

	filter_free(&filter, compiler);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the %s filter", label
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that no two siblings anywhere under the filter's memory_context
// share a name, for both the small and the large signature.
static int
test_no_sibling_name_collision(void *arena) {
	TEST_ASSERT_SUCCESS(
		check_no_sibling_collision(sign_small_compile, arena, "small"),
		"small signature"
	);
	TEST_ASSERT_SUCCESS(
		check_no_sibling_collision(sign_large_compile, arena, "large"),
		"large signature"
	);
	return TEST_SUCCESS;
}

// Verifies that an attribute's leaf context parents both its registry and
// its own payload as siblings: port_src has a "registry" child and a "port"
// child, rather than one nesting under the other.
static int
test_leaf_parents_registry_and_payload(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_filter(sign_small_compile, &fx.mctx, "filter", &filter),
		"failed to build small filter"
	);

	struct memory_context *port_src_ctx = NULL;
	for (struct memory_context *child = ctx_child(&filter.memory_context);
	     child != NULL;
	     child = ctx_sibling(child)) {
		if (strcmp(child->name, "port_src") == 0) {
			port_src_ctx = child;
			break;
		}
	}
	TEST_ASSERT(
		port_src_ctx != NULL, "no 'port_src' child under the filter"
	);

	TEST_ASSERT_EQUAL(
		direct_child_count(port_src_ctx, "registry"),
		1,
		"'port_src' has no 'registry' child"
	);
	TEST_ASSERT_EQUAL(
		direct_child_count(port_src_ctx, "port"),
		1,
		"'port_src' has no 'port' child"
	);

	filter_free(&filter, sign_small_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the small filter"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that no context name under the filter encodes a vertex index.
static int
test_no_index_shaped_names(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_filter(sign_large_compile, &fx.mctx, "filter", &filter),
		"failed to build large filter"
	);

	TEST_ASSERT_SUCCESS(
		no_index_syntax(&filter.memory_context),
		"index-shaped name found under the filter"
	);

	filter_free(&filter, sign_large_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the large filter"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that filter_free leaves the parent context childless and hands
// the allocator back every byte filter_init took.
static int
test_teardown_leaves_nothing_behind(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_filter(sign_large_compile, &fx.mctx, "filter", &filter),
		"failed to build large filter"
	);

	filter_free(&filter, sign_large_compile);

	TEST_ASSERT_NULL(
		ctx_child(&fx.mctx), "parent context still has children"
	);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the large filter"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that a net6_src/net6_dst filter has no sibling collision between
// the hi and lo lpm contexts collect_net6_range creates for each attribute.
static int
test_net6_no_sibling_collision(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter;
	TEST_ASSERT_SUCCESS(
		build_net6_filter(&fx.mctx, "filter", &filter),
		"failed to build net6 filter"
	);

	TEST_ASSERT_SUCCESS(
		no_duplicate_siblings(&filter.memory_context),
		"sibling name collision under the net6 filter"
	);

	filter_free(&filter, sign_net6_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the net6 filter"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that two filters built under one shared parent context, as ACL
// and friends do for their per-signature filters, do not collide as long as
// filter_init is given distinct names.
static int
test_two_filters_one_context(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);

	struct filter filter_a;
	TEST_ASSERT_SUCCESS(
		build_filter(
			sign_small_compile, &fx.mctx, "filter_a", &filter_a
		),
		"failed to build filter_a"
	);
	struct filter filter_b;
	TEST_ASSERT_SUCCESS(
		build_filter(
			sign_small_compile, &fx.mctx, "filter_b", &filter_b
		),
		"failed to build filter_b"
	);

	TEST_ASSERT_SUCCESS(
		no_duplicate_siblings(&fx.mctx),
		"sibling name collision between filter_a and filter_b"
	);

	filter_free(&filter_b, sign_small_compile);
	filter_free(&filter_a, sign_small_compile);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the two filters"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");
	void *arena = malloc(CTX_NAMES_ARENA_SIZE);
	int failed = 0;

	struct {
		const char *name;
		int (*func)(void *);
	} cases[] = {
		{"leaf_named_after_attribute", test_leaf_named_after_attribute},
		{"leaf_names_survive_signature_change",
		 test_leaf_names_survive_signature_change},
		{"no_sibling_name_collision", test_no_sibling_name_collision},
		{"leaf_parents_registry_and_payload",
		 test_leaf_parents_registry_and_payload},
		{"no_index_shaped_names", test_no_index_shaped_names},
		{"teardown_leaves_nothing_behind",
		 test_teardown_leaves_nothing_behind},
		{"net6_no_sibling_collision", test_net6_no_sibling_collision},
		{"two_filters_one_context", test_two_filters_one_context},
	};

	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		// The previous case's filter_free left its freed blocks
		// ASan-poisoned, so the arena needs unpoisoning before this
		// case's fill can touch it.
		asan_unpoison_memory_region(arena, CTX_NAMES_ARENA_SIZE);
		memset(arena, 0, CTX_NAMES_ARENA_SIZE);
		if (cases[i].func(arena) != TEST_SUCCESS) {
			LOG(ERROR, "%s failed", cases[i].name);
			failed = 1;
			continue;
		}
		LOG(INFO, "%s passed", cases[i].name);
	}

	// The last case leaves the arena poisoned. The call to free below
	// requires it unpoisoned first.
	asan_unpoison_memory_region(arena, CTX_NAMES_ARENA_SIZE);
	free(arena);

	if (failed) {
		LOG(ERROR, "context_names tests FAILED");
		return 1;
	}

	LOG(INFO, "All context_names tests passed");
	return 0;
}
