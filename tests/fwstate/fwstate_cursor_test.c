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

/*
 * Initialize a fwstate map configuration using real fw4 key/value types.
 */
static void
setup_fwstate_config(
	fwmap_config_t *cfg, uint32_t idx_size, uint32_t extra_buckets
) {
	memset(cfg, 0, sizeof(*cfg));
	cfg->key_size = sizeof(struct fw4_state_key);
	cfg->value_size = sizeof(struct fw_state_value);
	cfg->hash_seed = 0;
	cfg->worker_count = 1;
	cfg->hash_fn_id = FWMAP_HASH_FNV1A;
	cfg->key_equal_fn_id = FWMAP_KEY_EQUAL_FW4;
	cfg->rand_fn_id = FWMAP_RAND_DEFAULT;
	cfg->copy_key_fn_id = FWMAP_COPY_KEY_FW4;
	cfg->copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE;
	cfg->merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE;
	cfg->index_size = idx_size;
	cfg->extra_bucket_count = extra_buckets;
}

/*
 * Helper: create a fw4 key with distinct fields.
 */
static struct fw4_state_key
make_fw4_key(
	uint16_t proto,
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t src_addr,
	uint32_t dst_addr
) {
	struct fw4_state_key key;
	memset(&key, 0, sizeof(key));
	key.hdr.proto = proto;
	key.hdr.src_port = src_port;
	key.hdr.dst_port = dst_port;
	key.src_addr = src_addr;
	key.dst_addr = dst_addr;
	return key;
}

/*
 * Helper: create a fw_state_value.
 */
static struct fw_state_value
make_fw_value(
	uint8_t src_flags,
	uint8_t dst_flags,
	uint64_t created_at,
	uint64_t updated_at
) {
	struct fw_state_value val;
	memset(&val, 0, sizeof(val));
	val.flags.tcp.src = src_flags;
	val.flags.tcp.dst = dst_flags;
	val.created_at = created_at;
	val.updated_at = updated_at;
	val.packets_forward = 1;
	val.packets_backward = 0;
	return val;
}

/*
 * Helper: insert a fw4 entry into the map.
 * Returns the index assigned to the entry.
 */
static int64_t
insert_fw4_entry(
	fwmap_t *map, struct fw4_state_key *key, struct fw_state_value *value
) {
	return fwmap_put(map, WORKER_ID, now, DEFAULT_TTL, key, value, NULL);
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
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "empty_map");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	fwstate_cursor_entry_t out[10];

	/* Forward on empty map */
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};
	int32_t n = fwstate_cursor_read_forward(map, &cursor, now, out, 10);
	assert(n == 0);

	/* Backward on empty map */
	cursor.key_pos = UINT32_MAX;
	n = fwstate_cursor_read_backward(map, &cursor, now, out, 10);
	assert(n == 0);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "empty_map");
	printf("  Empty map test passed\n");
}

/* ====================================================================== */
/* Test 3: Forward iteration                                               */
/* ====================================================================== */

static void
test_forward_iteration(void *arena) {
	printf("\n--- Forward Iteration Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "forward_iter");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 5 entries with distinct keys */
	struct fw4_state_key keys[5];
	struct fw_state_value vals[5];
	for (int i = 0; i < 5; i++) {
		keys[i] = make_fw4_key(
			IPPROTO_TCP,
			(uint16_t)(1000 + i),
			80,
			0x0A000001 + (uint32_t)i,
			0xC0A80001
		);
		vals[i] = make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
		int64_t ret = insert_fw4_entry(map, &keys[i], &vals[i]);
		assert(ret >= 0);
	}

	/* Read all forward */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int32_t n = fwstate_cursor_read_forward(map, &cursor, now, out, 10);
	assert(n == 5);

	/* Verify ascending idx order and key data */
	for (int i = 0; i < 5; i++) {
		assert(out[i].idx == (uint32_t)i);
		struct fw4_state_key *k = (struct fw4_state_key *)out[i].key;
		assert(k->hdr.src_port == (uint16_t)(1000 + i));
		assert(k->hdr.dst_port == 80);
		assert(k->hdr.proto == IPPROTO_TCP);
	}

	/* Cursor should advance to 5 */
	assert(cursor.key_pos == 5);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "forward_iter");
	printf("  Forward iteration test passed\n");
}

/* ====================================================================== */
/* Test 4: Backward iteration                                              */
/* ====================================================================== */

static void
test_backward_iteration(void *arena) {
	printf("\n--- Backward Iteration Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "backward_iter");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 5 entries */
	struct fw4_state_key keys[5];
	struct fw_state_value vals[5];
	for (int i = 0; i < 5; i++) {
		keys[i] = make_fw4_key(
			IPPROTO_TCP,
			(uint16_t)(2000 + i),
			443,
			0x0A000001 + (uint32_t)i,
			0xC0A80002
		);
		vals[i] = make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
		int64_t ret = insert_fw4_entry(map, &keys[i], &vals[i]);
		assert(ret >= 0);
	}

	/* Read backward from UINT32_MAX (should clamp to last entry) */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = UINT32_MAX,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int32_t n = fwstate_cursor_read_backward(map, &cursor, now, out, 10);
	assert(n == 5);

	/* Verify descending idx order (4..0) */
	for (int i = 0; i < 5; i++) {
		assert(out[i].idx == (uint32_t)(4 - i));
		struct fw4_state_key *k = (struct fw4_state_key *)out[i].key;
		assert(k->hdr.src_port == (uint16_t)(2000 + 4 - i));
	}

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "backward_iter");
	printf("  Backward iteration test passed\n");
}

/* ====================================================================== */
/* Test 5: Expired entries skipped                                         */
/* ====================================================================== */

static void
test_expired_skipped(void *arena) {
	printf("\n--- Expired Entries Skipped Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "expired_skip");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 3 entries: TCP, UDP, TCP */
	struct fw4_state_key k0 =
		make_fw4_key(IPPROTO_TCP, 3000, 80, 0x0A000001, 0xC0A80001);
	struct fw_state_value v0 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k0, &v0) >= 0);

	struct fw4_state_key k1 =
		make_fw4_key(IPPROTO_UDP, 3001, 53, 0x0A000002, 0xC0A80001);
	struct fw_state_value v1 = make_fw_value(0, 0, now, now);
	assert(insert_fw4_entry(map, &k1, &v1) >= 0);

	struct fw4_state_key k2 =
		make_fw4_key(IPPROTO_TCP, 3002, 443, 0x0A000003, 0xC0A80001);
	struct fw_state_value v2 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k2, &v2) >= 0);

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

	int32_t n =
		fwstate_cursor_read_forward(map, &cursor, read_now, out, 10);
	assert(n == 2);

	/* Both returned entries should have keys with TCP proto */
	for (int i = 0; i < 2; i++) {
		struct fw4_state_key *key = (struct fw4_state_key *)out[i].key;
		assert(key->hdr.proto == IPPROTO_TCP);
	}

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "expired_skip");
	printf("  Expired entries skipped test passed\n");
}

/* ====================================================================== */
/* Test 6: include_expired=true                                            */
/* ====================================================================== */

static void
test_include_expired(void *arena) {
	printf("\n--- Include Expired Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "include_exp");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Same setup as expired test: TCP, UDP, TCP */
	struct fw4_state_key k0 =
		make_fw4_key(IPPROTO_TCP, 4000, 80, 0x0A000001, 0xC0A80001);
	struct fw_state_value v0 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k0, &v0) >= 0);

	struct fw4_state_key k1 =
		make_fw4_key(IPPROTO_UDP, 4001, 53, 0x0A000002, 0xC0A80001);
	struct fw_state_value v1 = make_fw_value(0, 0, now, now);
	assert(insert_fw4_entry(map, &k1, &v1) >= 0);

	struct fw4_state_key k2 =
		make_fw4_key(IPPROTO_TCP, 4002, 443, 0x0A000003, 0xC0A80001);
	struct fw_state_value v2 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k2, &v2) >= 0);

	/* Advance time so UDP is expired */
	uint64_t read_now = now + (uint64_t)31e9;

	/* Forward with include_expired=true: should return all 3 */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int32_t n =
		fwstate_cursor_read_forward(map, &cursor, read_now, out, 10);
	assert(n == 3);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "include_exp");
	printf("  Include expired test passed\n");
}

/* ====================================================================== */
/* Test 7: Uninitialized entries skipped                                   */
/* ====================================================================== */

static void
test_uninitialized_skipped(void *arena) {
	printf("\n--- Uninitialized Entries Skipped Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "uninit_skip");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 3 entries */
	struct fw4_state_key k0 =
		make_fw4_key(IPPROTO_TCP, 5000, 80, 0x0A000001, 0xC0A80001);
	struct fw_state_value v0 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k0, &v0) >= 0);

	struct fw4_state_key k1 =
		make_fw4_key(IPPROTO_TCP, 5001, 80, 0x0A000002, 0xC0A80001);
	struct fw_state_value v1 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k1, &v1) >= 0);

	struct fw4_state_key k2 =
		make_fw4_key(IPPROTO_TCP, 5002, 80, 0x0A000003, 0xC0A80001);
	struct fw_state_value v2 =
		make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
	assert(insert_fw4_entry(map, &k2, &v2) >= 0);

	/* Zero out updated_at of middle entry to simulate uninitialized */
	struct fw_state_value *mid_val =
		(struct fw_state_value *)fwmap_get_value(map, 1);
	assert(mid_val != NULL);
	mid_val->updated_at = 0;

	/* Even with include_expired=true, uninitialized entries are skipped */
	fwstate_cursor_entry_t out[10];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int32_t n = fwstate_cursor_read_forward(map, &cursor, now, out, 10);
	assert(n == 2);

	/* Verify we got entries 0 and 2 (skipped 1) */
	assert(out[0].idx == 0);
	assert(out[1].idx == 2);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "uninit_skip");
	printf("  Uninitialized entries skipped test passed\n");
}

/* ====================================================================== */
/* Test 8: Paging                                                          */
/* ====================================================================== */

static void
test_paging(void *arena) {
	printf("\n--- Paging Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "paging");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 10 entries */
	for (int i = 0; i < 10; i++) {
		struct fw4_state_key key = make_fw4_key(
			IPPROTO_TCP,
			(uint16_t)(6000 + i),
			80,
			0x0A000001 + (uint32_t)i,
			0xC0A80001
		);
		struct fw_state_value val =
			make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
		assert(insert_fw4_entry(map, &key, &val) >= 0);
	}

	/* Read in batches of 3 */
	fwstate_cursor_entry_t out[3];
	fwstate_cursor_t cursor = {
		.key_pos = 0,
		.include_expired = true,
		.timeouts = test_timeouts,
	};

	int total = 0;

	/* Batch 1: entries 0,1,2 */
	int32_t n = fwstate_cursor_read_forward(map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 0);
	assert(out[2].idx == 2);
	assert(cursor.key_pos == 3);
	total += n;

	/* Batch 2: entries 3,4,5 */
	n = fwstate_cursor_read_forward(map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 3);
	assert(out[2].idx == 5);
	assert(cursor.key_pos == 6);
	total += n;

	/* Batch 3: entries 6,7,8 */
	n = fwstate_cursor_read_forward(map, &cursor, now, out, 3);
	assert(n == 3);
	assert(out[0].idx == 6);
	assert(out[2].idx == 8);
	assert(cursor.key_pos == 9);
	total += n;

	/* Batch 4: entry 9 only */
	n = fwstate_cursor_read_forward(map, &cursor, now, out, 3);
	assert(n == 1);
	assert(out[0].idx == 9);
	assert(cursor.key_pos == 10);
	total += n;

	/* Batch 5: no more entries */
	n = fwstate_cursor_read_forward(map, &cursor, now, out, 3);
	assert(n == 0);

	assert(total == 10);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "paging");
	printf("  Paging test passed\n");
}

/* ====================================================================== */
/* Test 9: Forward bounds safety                                           */
/* ====================================================================== */

static void
test_forward_bounds(void *arena) {
	printf("\n--- Forward Bounds Safety Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "fwd_bounds");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 3 entries so key_cursor = 3 */
	for (int i = 0; i < 3; i++) {
		struct fw4_state_key key = make_fw4_key(
			IPPROTO_TCP,
			(uint16_t)(7000 + i),
			80,
			0x0A000001 + (uint32_t)i,
			0xC0A80001
		);
		struct fw_state_value val =
			make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
		assert(insert_fw4_entry(map, &key, &val) >= 0);
	}

	fwstate_cursor_entry_t out[10];

	/* key_pos == key_cursor: returns 0 */
	fwstate_cursor_t cursor = {
		.key_pos = 3,
		.include_expired = true,
		.timeouts = test_timeouts,
	};
	int32_t n = fwstate_cursor_read_forward(map, &cursor, now, out, 10);
	assert(n == 0);

	/* key_pos > key_cursor: returns 0 */
	cursor.key_pos = 100;
	n = fwstate_cursor_read_forward(map, &cursor, now, out, 10);
	assert(n == 0);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "fwd_bounds");
	printf("  Forward bounds safety test passed\n");
}

/* ====================================================================== */
/* Test 10: Backward clamping                                              */
/* ====================================================================== */

static void
test_backward_clamping(void *arena) {
	printf("\n--- Backward Clamping Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "bwd_clamp");

	fwmap_config_t cfg;
	setup_fwstate_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	now = 1000;

	/* Insert 3 entries so key_cursor = 3 */
	for (int i = 0; i < 3; i++) {
		struct fw4_state_key key = make_fw4_key(
			IPPROTO_TCP,
			(uint16_t)(8000 + i),
			80,
			0x0A000001 + (uint32_t)i,
			0xC0A80001
		);
		struct fw_state_value val =
			make_fw_value(FWSTATE_ACK, FWSTATE_ACK, now, now);
		assert(insert_fw4_entry(map, &key, &val) >= 0);
	}

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

	int32_t n = fwstate_cursor_read_backward(map, &cursor, now, out, 10);
	assert(n == 3);
	assert(out[0].idx == 2);
	assert(out[1].idx == 1);
	assert(out[2].idx == 0);

	/* Verify port values match expected reverse order */
	struct fw4_state_key *k0 = (struct fw4_state_key *)out[0].key;
	struct fw4_state_key *k2 = (struct fw4_state_key *)out[2].key;
	assert(k0->hdr.src_port == 8002);
	assert(k2->hdr.src_port == 8000);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "bwd_clamp");
	printf("  Backward clamping test passed\n");
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

	free_arena(arena, ARENA_SIZE);

	printf("\n%s%s=== All tests passed ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return EXIT_SUCCESS;
}
