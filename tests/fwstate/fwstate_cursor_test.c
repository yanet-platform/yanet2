/*
 * FWState Cursor Tests
 *
 * Tests the cursor-based iteration of fwstate map entries including
 * forward/backward traversal, TTL filtering, expired entry handling,
 * paging, and boundary conditions.
 */

#include "common/memory.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/types.h"
#include "test_utils.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>

#define ARENA_SIZE_MB 64
#define ARENA_SIZE ((1 << 20) * ARENA_SIZE_MB)
#define DEFAULT_TTL 50000
#define WORKER_ID 0

/* Global time counter for TTL expiration testing */
volatile uint64_t now = 0;

/* Standard timeouts for cursor tests (in nanoseconds) */
static struct fwstate_timeouts test_timeouts = {
	.tcp_syn_ack = 120e9, /* 120s */
	.tcp_syn = 120e9,     /* 120s */
	.tcp_fin = 120e9,     /* 120s */
	.tcp = 120e9,	      /* 120s */
	.udp = 30e9,	      /* 30s */
	.default_ = 16e9,     /* 16s */
};

/* ====================================================================== */
/* Test environment: context + config + map lifecycle                       */
/* ====================================================================== */

typedef struct test_env {
	struct memory_context *ctx;
	fwmap_t *map;
	const char *name;
} test_env_t;

/*
 * Create a test environment: memory context, fwstate config, and map.
 * Sets the global `now` to 1000.
 */
static test_env_t
test_env_create(void *arena, const char *name) {
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, name);

	fwmap_config_t cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.key_size = sizeof(struct fw4_state_key);
	cfg.value_size = sizeof(struct fw_state_value);
	cfg.hash_seed = 0;
	cfg.worker_count = 1;
	cfg.hash_fn_id = FWMAP_HASH_FNV1A;
	cfg.key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
	cfg.rand_fn_id = FWMAP_RAND_DEFAULT;
	cfg.copy_key_fn_id = FWMAP_COPY_KEY_FW4;
	cfg.copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE;
	cfg.merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE;
	cfg.index_size = 128;
	cfg.extra_bucket_count = 8;

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	return (test_env_t){.ctx = ctx, .map = map, .name = name};
}

/*
 * Destroy the map and verify no memory leaks.
 */
static void
test_env_destroy(test_env_t *env) {
	fwmap_destroy(env->map, env->ctx);
	verify_memory_leaks(env->ctx, env->name);
}

/*
 * Insert `count` TCP entries with sequential source ports starting at
 * `base_port`, all targeting `dst_port`. Source addresses increment from
 * 10.0.0.1. TCP flags are set to ACK/ACK (established).
 */
static void
test_env_insert_tcp(
	test_env_t *env, uint32_t count, uint16_t base_port, uint16_t dst_port
) {
	for (uint32_t i = 0; i < count; i++) {
		struct fw4_state_key key;
		memset(&key, 0, sizeof(key));
		key.hdr.proto = IPPROTO_TCP;
		key.hdr.src_port = (uint16_t)(base_port + i);
		key.hdr.dst_port = dst_port;
		key.src_addr = 0x0A000001 + i;
		key.dst_addr = 0xC0A80001;

		struct fw_state_value val;
		memset(&val, 0, sizeof(val));
		val.flags.tcp.src = FWSTATE_ACK;
		val.flags.tcp.dst = FWSTATE_ACK;
		val.created_at = now;
		val.updated_at = now;
		val.packets_forward = 1;

		int64_t ret = fwmap_put(
			env->map, WORKER_ID, now, DEFAULT_TTL, &key, &val, NULL
		);
		assert(ret >= 0);
	}
}

/*
 * Insert `count` UDP entries with sequential source ports starting at
 * `base_port`, all targeting `dst_port`. Source addresses increment from
 * 10.0.0.1 + addr_offset.
 */
static void
test_env_insert_udp(
	test_env_t *env,
	uint32_t count,
	uint16_t base_port,
	uint16_t dst_port,
	uint32_t addr_offset
) {
	for (uint32_t i = 0; i < count; i++) {
		struct fw4_state_key key;
		memset(&key, 0, sizeof(key));
		key.hdr.proto = IPPROTO_UDP;
		key.hdr.src_port = (uint16_t)(base_port + i);
		key.hdr.dst_port = dst_port;
		key.src_addr = 0x0A000001 + addr_offset + i;
		key.dst_addr = 0xC0A80001;

		struct fw_state_value val;
		memset(&val, 0, sizeof(val));
		val.created_at = now;
		val.updated_at = now;
		val.packets_forward = 1;

		int64_t ret = fwmap_put(
			env->map, WORKER_ID, now, DEFAULT_TTL, &key, &val, NULL
		);
		assert(ret >= 0);
	}
}

/* ====================================================================== */
/* Test 1: TTL selection                                                   */
/* ====================================================================== */

static void
test_ttl_selection(void) {
	printf("\n--- TTL Selection Test ---\n");

	union fw_state_flags_u flags;
	memset(&flags, 0, sizeof(flags));

	/* TCP SYN only */
	flags.tcp.src = FWSTATE_SYN;
	flags.tcp.dst = 0;
	assert(fwstate_entry_ttl(IPPROTO_TCP, flags.raw, &test_timeouts) ==
	       test_timeouts.tcp_syn);

	/* TCP SYN-ACK */
	flags.tcp.src = FWSTATE_SYN;
	flags.tcp.dst = FWSTATE_ACK;
	assert(fwstate_entry_ttl(IPPROTO_TCP, flags.raw, &test_timeouts) ==
	       test_timeouts.tcp_syn_ack);

	/* TCP FIN (src side) */
	flags.tcp.src = FWSTATE_FIN;
	flags.tcp.dst = 0;
	assert(fwstate_entry_ttl(IPPROTO_TCP, flags.raw, &test_timeouts) ==
	       test_timeouts.tcp_fin);

	/* TCP FIN (dst side) */
	flags.tcp.src = 0;
	flags.tcp.dst = FWSTATE_FIN;
	assert(fwstate_entry_ttl(IPPROTO_TCP, flags.raw, &test_timeouts) ==
	       test_timeouts.tcp_fin);

	/* TCP established (no special flags) */
	flags.tcp.src = FWSTATE_ACK;
	flags.tcp.dst = FWSTATE_ACK;
	assert(fwstate_entry_ttl(IPPROTO_TCP, flags.raw, &test_timeouts) ==
	       test_timeouts.tcp);

	/* UDP */
	flags.raw = 0;
	assert(fwstate_entry_ttl(IPPROTO_UDP, flags.raw, &test_timeouts) ==
	       test_timeouts.udp);

	/* Default (ICMP) */
	assert(fwstate_entry_ttl(IPPROTO_ICMP, flags.raw, &test_timeouts) ==
	       test_timeouts.default_);

	printf("  TTL selection test passed\n");
}

/* ====================================================================== */
/* Test 2: Empty map                                                       */
/* ====================================================================== */

static void
test_empty_map(void *arena) {
	printf("\n--- Empty Map Test ---\n");
	test_env_t env = test_env_create(arena, "empty_map");

	fwstate_cursor_entry_t out[10];

	/* Forward on empty map */
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};
	uint32_t n =
		fwstate_cursor_read_forward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	/* Backward on empty map */
	cursor.key_pos = INT64_MAX;
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	test_env_destroy(&env);
	printf("  Empty map test passed\n");
}

/* ====================================================================== */
/* Test 3: Forward iteration                                               */
/* ====================================================================== */

static void
test_forward_iteration(void *arena) {
	printf("\n--- Forward Iteration Test ---\n");
	test_env_t env = test_env_create(arena, "forward_iter");

	/* Insert 5 TCP entries: ports 1000..1004 -> 80 */
	test_env_insert_tcp(&env, 5, 1000, 80);

	/* Read all forward */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n =
		fwstate_cursor_read_forward(env.map, &cursor, now, out, 10);
	assert(n == 5);

	/* Verify ascending idx order and key data */
	for (uint32_t i = 0; i < 5; i++) {
		assert(out[i].idx == i);
		struct fw4_state_key *k = (struct fw4_state_key *)out[i].key;
		assert(k->hdr.src_port == (uint16_t)(1000 + i));
		assert(k->hdr.dst_port == 80);
		assert(k->hdr.proto == IPPROTO_TCP);
	}

	/* Cursor should advance to 5 */
	assert(cursor.key_pos == 5);

	test_env_destroy(&env);
	printf("  Forward iteration test passed\n");
}

/* ====================================================================== */
/* Test 4: Backward iteration                                              */
/* ====================================================================== */

static void
test_backward_iteration(void *arena) {
	printf("\n--- Backward Iteration Test ---\n");
	test_env_t env = test_env_create(arena, "backward_iter");

	/* Insert 5 TCP entries: ports 2000..2004 -> 443 */
	test_env_insert_tcp(&env, 5, 2000, 443);

	/* Read backward from INT64_MAX (should clamp to last entry) */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = INT64_MAX,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n =
		fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 5);

	/* Verify descending idx order (4..0), including entry at index 0 */
	for (uint32_t i = 0; i < 5; i++) {
		assert(out[i].idx == (4 - i));
		struct fw4_state_key *k = (struct fw4_state_key *)out[i].key;
		assert(k->hdr.src_port == (uint16_t)(2000 + 4 - i));
	}

	/* After exhaustion, key_pos should be -1 */
	assert(cursor.key_pos == -1);

	/* Subsequent call must return 0 (no infinite loop) */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	test_env_destroy(&env);
	printf("  Backward iteration test passed\n");
}

/* ====================================================================== */
/* Test 5: Expired entries skipped                                         */
/* ====================================================================== */

static void
test_expired_skipped(void *arena) {
	printf("\n--- Expired Entries Skipped Test ---\n");
	test_env_t env = test_env_create(arena, "expired_skip");

	/* Insert: TCP(3000->80), UDP(3001->53), TCP(3002->443) */
	test_env_insert_tcp(&env, 1, 3000, 80);
	test_env_insert_udp(&env, 1, 3001, 53, 1);
	test_env_insert_tcp(&env, 1, 3002, 443);

	/*
	 * Advance time so UDP (30s TTL) is expired but TCP (120s TTL)
	 * is not. Use nanosecond values since timeouts are in nanoseconds.
	 */
	uint64_t read_now = now + (uint64_t)31e9; /* 31 seconds later */

	/* Forward with include_expired=false: should skip the UDP entry */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = false,
		.timeouts = test_timeouts,
	};

	uint32_t n = fwstate_cursor_read_forward(
		env.map, &cursor, read_now, out, 10
	);
	assert(n == 2);

	/* Both returned entries should have keys with TCP proto */
	for (int i = 0; i < 2; i++) {
		struct fw4_state_key *key = (struct fw4_state_key *)out[i].key;
		assert(key->hdr.proto == IPPROTO_TCP);
	}

	test_env_destroy(&env);
	printf("  Expired entries skipped test passed\n");
}

/* ====================================================================== */
/* Test 6: include_expired=true                                            */
/* ====================================================================== */

static void
test_include_expired(void *arena) {
	printf("\n--- Include Expired Test ---\n");
	test_env_t env = test_env_create(arena, "include_exp");

	/* Same setup as expired test: TCP(4000->80), UDP(4001->53),
	 * TCP(4002->443) */
	test_env_insert_tcp(&env, 1, 4000, 80);
	test_env_insert_udp(&env, 1, 4001, 53, 1);
	test_env_insert_tcp(&env, 1, 4002, 443);

	/* Advance time so UDP is expired */
	uint64_t read_now = now + (uint64_t)31e9;

	/* Forward with include_expired=true: should return all 3 */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n = fwstate_cursor_read_forward(
		env.map, &cursor, read_now, out, 10
	);
	assert(n == 3);

	test_env_destroy(&env);
	printf("  Include expired test passed\n");
}

/* ====================================================================== */
/* Test 7: Uninitialized entries skipped                                   */
/* ====================================================================== */

static void
test_uninitialized_skipped(void *arena) {
	printf("\n--- Uninitialized Entries Skipped Test ---\n");
	test_env_t env = test_env_create(arena, "uninit_skip");

	/* Insert 3 TCP entries: ports 5000..5002 -> 80 */
	test_env_insert_tcp(&env, 3, 5000, 80);

	/* Zero out updated_at of middle entry to simulate uninitialized */
	struct fw_state_value *mid_val =
		(struct fw_state_value *)fwmap_get_value(env.map, 1);
	assert(mid_val != NULL);
	mid_val->updated_at = 0;

	/* Even with include_expired=true, uninitialized entries are skipped */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n =
		fwstate_cursor_read_forward(env.map, &cursor, now, out, 10);
	assert(n == 2);

	/* Verify we got entries 0 and 2 (skipped 1) */
	assert(out[0].idx == 0);
	assert(out[1].idx == 2);

	test_env_destroy(&env);
	printf("  Uninitialized entries skipped test passed\n");
}

/* ====================================================================== */
/* Test 8: Paging                                                          */
/* ====================================================================== */

static void
test_paging(void *arena) {
	printf("\n--- Paging Test ---\n");
	test_env_t env = test_env_create(arena, "paging");

	/* Insert 10 TCP entries: ports 6000..6009 -> 80 */
	test_env_insert_tcp(&env, 10, 6000, 80);

	/* Read in batches of 3 */
	fwstate_cursor_entry_t out[3];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int total = 0;

	/* Batch 1: entries 0,1,2 */
	uint32_t n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 0);
	assert(out[2].idx == 2);
	assert(cursor.key_pos == 3);
	total += n;

	/* Batch 2: entries 3,4,5 */
	n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 3);
	assert(out[2].idx == 5);
	assert(cursor.key_pos == 6);
	total += n;

	/* Batch 3: entries 6,7,8 */
	n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 6);
	assert(out[2].idx == 8);
	assert(cursor.key_pos == 9);
	total += n;

	/* Batch 4: entry 9 only */
	n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 3);
	assert(n == 1);
	assert(out[0].idx == 9);
	assert(cursor.key_pos == 10);
	total += n;

	/* Batch 5: no more entries */
	n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 3);
	assert(n == 0);

	assert(total == 10);

	test_env_destroy(&env);
	printf("  Paging test passed\n");
}

/* ====================================================================== */
/* Test 9: Forward bounds safety                                           */
/* ====================================================================== */

static void
test_forward_bounds(void *arena) {
	printf("\n--- Forward Bounds Safety Test ---\n");
	test_env_t env = test_env_create(arena, "fwd_bounds");

	/* Insert 3 TCP entries: ports 7000..7002 -> 80, so key_cursor = 3 */
	test_env_insert_tcp(&env, 3, 7000, 80);

	fwstate_cursor_entry_t out[10];

	/* key_pos == key_cursor: returns 0 */
	fwstate_cursor_t cursor = {
		.key_pos = 3,
		.include_expired = true,
		.timeouts = test_timeouts,
	};
	uint32_t n =
		fwstate_cursor_read_forward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	/* key_pos > key_cursor: returns 0 */
	cursor.key_pos = 100;
	n = fwstate_cursor_read_forward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	test_env_destroy(&env);
	printf("  Forward bounds safety test passed\n");
}

/* ====================================================================== */
/* Test 10: Backward clamping                                              */
/* ====================================================================== */

static void
test_backward_clamping(void *arena) {
	printf("\n--- Backward Clamping Test ---\n");
	test_env_t env = test_env_create(arena, "bwd_clamp");

	/* Insert 3 TCP entries: ports 8000..8002 -> 80, so key_cursor = 3 */
	test_env_insert_tcp(&env, 3, 8000, 80);

	fwstate_cursor_entry_t out[10];

	/*
	 * key_pos > key_cursor should clamp to key_cursor-1 (= 2).
	 * Should return entries 2, 1, 0 in that order.
	 */
	fwstate_cursor_t cursor = {
		.key_pos = 999,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n =
		fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 3);
	assert(out[0].idx == 2);
	assert(out[1].idx == 1);
	assert(out[2].idx == 0);

	/* Verify port values match expected reverse order */
	struct fw4_state_key *k0 = (struct fw4_state_key *)out[0].key;
	struct fw4_state_key *k2 = (struct fw4_state_key *)out[2].key;
	assert(k0->hdr.src_port == 8002);
	assert(k2->hdr.src_port == 8000);

	test_env_destroy(&env);
	printf("  Backward clamping test passed\n");
}

/* ====================================================================== */
/* Test 11: Single entry backward                                          */
/* ====================================================================== */

static void
test_single_entry_backward(void *arena) {
	printf("\n--- Single Entry Backward Test ---\n");
	test_env_t env = test_env_create(arena, "single_bwd");

	/* Insert exactly 1 TCP entry: port 11000 -> 80 */
	test_env_insert_tcp(&env, 1, 11000, 80);

	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = INT64_MAX,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	uint32_t n =
		fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 1);
	assert(out[0].idx == 0);

	/* Cursor should be exhausted */
	assert(cursor.key_pos == -1);

	/* Next call should return 0 entries */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 10);
	assert(n == 0);

	test_env_destroy(&env);
	printf("  Single entry backward test passed\n");
}

/* ====================================================================== */
/* Test 12: Backward paging                                                */
/* ====================================================================== */

static void
test_backward_paging(void *arena) {
	printf("\n--- Backward Paging Test ---\n");
	test_env_t env = test_env_create(arena, "bwd_paging");

	/* Insert 10 TCP entries: ports 12000..12009 -> 80 */
	test_env_insert_tcp(&env, 10, 12000, 80);

	/* Read backward in batches of 3 */
	fwstate_cursor_entry_t out[3];
	fwstate_cursor_t cursor = {
		.key_pos = INT64_MAX,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int total = 0;

	/* Batch 1: entries 9, 8, 7 */
	uint32_t n =
		fwstate_cursor_read_backward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 9);
	assert(out[1].idx == 8);
	assert(out[2].idx == 7);
	total += n;

	/* Batch 2: entries 6, 5, 4 */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 6);
	assert(out[1].idx == 5);
	assert(out[2].idx == 4);
	total += n;

	/* Batch 3: entries 3, 2, 1 */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 3);
	assert(out[1].idx == 2);
	assert(out[2].idx == 1);
	total += n;

	/* Batch 4: entry 0 only */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 3);
	assert(n == 1);
	assert(out[0].idx == 0);
	total += n;

	/* Batch 5: no more entries */
	n = fwstate_cursor_read_backward(env.map, &cursor, now, out, 3);
	assert(n == 0);

	assert(total == 10);

	test_env_destroy(&env);
	printf("  Backward paging test passed\n");
}

/* ====================================================================== */
/* Test 13: Expired entry at index 0 in backward with include_expired      */
/* ====================================================================== */

static void
test_backward_expired_at_zero(void *arena) {
	printf("\n--- Backward Expired at Index 0 Test ---\n");
	test_env_t env = test_env_create(arena, "bwd_exp0");

	/* Entry 0: UDP (30s TTL) -- will expire */
	test_env_insert_udp(&env, 1, 14000, 53, 0);

	/* Entry 1: TCP (120s TTL) -- will not expire */
	test_env_insert_tcp(&env, 1, 14001, 80);

	/* Advance time past UDP TTL */
	uint64_t read_now = now + (uint64_t)31e9;

	/* Backward with include_expired=false: should skip entry 0 (UDP) */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = INT64_MAX,
		.include_expired = false,
		.timeouts = test_timeouts,
	};

	uint32_t n = fwstate_cursor_read_backward(
		env.map, &cursor, read_now, out, 10
	);
	assert(n == 1);
	assert(out[0].idx == 1); /* Only TCP entry */

	/* Backward with include_expired=true: should return both */
	cursor.key_pos = INT64_MAX;
	cursor.include_expired = true;
	n = fwstate_cursor_read_backward(env.map, &cursor, read_now, out, 10);
	assert(n == 2);
	assert(out[0].idx == 1);
	assert(out[1].idx == 0);
	assert(out[1].expired == true);

	test_env_destroy(&env);
	printf("  Backward expired at index 0 test passed\n");
}

/* ====================================================================== */
/* Main                                                                    */
/* ====================================================================== */

int
main(void) {
	printf("%s%s=== FWState Cursor Tests ===%s\n\n",
	       C_BOLD,
	       C_WHITE,
	       C_RESET);

	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (!arena) {
		fprintf(stderr,
			"Failed to allocate %dMB test arena\n",
			ARENA_SIZE_MB);
		return EXIT_FAILURE;
	}

	printf("%s%sRunning test suite...%s\n", C_BOLD, C_BLUE, C_RESET);

	test_ttl_selection();
	test_empty_map(arena);
	test_forward_iteration(arena);
	test_backward_iteration(arena);
	test_expired_skipped(arena);
	test_include_expired(arena);
	test_uninitialized_skipped(arena);
	test_paging(arena);
	test_forward_bounds(arena);
	test_backward_clamping(arena);
	test_single_entry_backward(arena);
	test_backward_paging(arena);
	test_backward_expired_at_zero(arena);

	free_arena(arena, ARENA_SIZE);

	printf("\n%s%s=== All tests passed ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return EXIT_SUCCESS;
}
