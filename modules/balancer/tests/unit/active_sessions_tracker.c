/*
 * Unit tests for active_sessions_tracker.
 *
 * Constants:
 *   ACTIVE_SESSIONS_TRACKER_PRECISION = 16
 *   active_sessions_tracker_now(ts)   = ts / 16
 *   active_sessions_tracker_until(ts) = (ts + 15) / 16
 *
 * shard->count tracks the running total of active sessions.
 * new_session()    adds make()'s return value to count.
 * prolong_session() adds prolong()'s return value to count.
 */

#include "../../../../common/memory.h"
#include "../../../../common/memory_block.h"
#include "../../../../common/test_assert.h"
#include "../../controlplane/state/active_sessions.h"
#include "../../dataplane/active_sessions.h"
#include "lib/logging/log.h"
#include <stdint.h>
#include <string.h>

/* ------------------------------------------------------------------ */
/* Memory setup helpers                                                */
/* ------------------------------------------------------------------ */

/*
 * A small static arena large enough for a few tracker shards.
 * sizeof(active_sessions_tracker_shard) = 64 bytes (cache-line aligned).
 * 8 shards = 512 bytes; add padding for alignment.
 */
#define TEST_ARENA_SIZE (4096)

static uint8_t g_arena[TEST_ARENA_SIZE] __attribute__((aligned(64)));

static void
setup_mctx(struct block_allocator *ba, struct memory_context *mctx) {
	block_allocator_init(ba);
	block_allocator_put_arena(ba, g_arena, TEST_ARENA_SIZE);
	memory_context_init(mctx, "test", ba);
}

/* ------------------------------------------------------------------ */
/* Test 1: create initialises all shards to count=0                    */
/* ------------------------------------------------------------------ */
static int
test_create_initialises_shards(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 3;
	const uint32_t now = 0;

	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, now);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	for (size_t i = 0; i < shards; ++i) {
		TEST_ASSERT_EQUAL(
			(int64_t)tracker[i].count,
			0,
			"shard[%zu].count should be 0 after create",
			i
		);
	}

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 2: new_session increments count by 1                           */
/* ------------------------------------------------------------------ */
static int
test_new_session_increments_count(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 2;
	/*
	 * now=0, timeout=32:
	 *   now_tick  = 0/16 = 0
	 *   until_tick = (0+32+15)/16 = 47/16 = 2
	 *   make(0, 2): diff[0]+=1 consumed -> +1; diff[2]-=1 pending
	 *   count += 1  =>  count = 1
	 */
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	active_sessions_tracker_new_session(tracker, 0, 0, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		1,
		"count should be 1 after one new_session"
	);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[1].count, 0, "shard[1].count should remain 0"
	);

	/* Second session on shard 0 at the same timestamp. */
	active_sessions_tracker_new_session(tracker, 0, 0, 48);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		2,
		"count should be 2 after two new_sessions on shard 0"
	);

	/* Session on shard 1. */
	active_sessions_tracker_new_session(tracker, 1, 0, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[1].count,
		1,
		"shard[1].count should be 1 after one new_session"
	);

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 3: session expires and count decrements                        */
/* ------------------------------------------------------------------ */
static int
test_session_expires_decrements_count(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 1;
	/*
	 * Create session at ts=0 with timeout=32:
	 *   now_tick=0, until_tick=(0+32+15)/16=2
	 *   make(0,2): +1 -> count=1; diff[2]=-1 pending
	 *
	 * Advance to ts=32 (tick=2) by creating a new session:
	 *   now_tick=32/16=2, until_tick=(32+32+15)/16=79/16=4
	 *   make(2,4): advance sweeps [0..2): slot 0 (=0), slot 1 (=0);
	 *              consume slot 2 (=-1) -> change=-1
	 *              diff[2]+=1 -> 0; diff[4]-=1
	 *              return 0 + (-1) = -1
	 *   count += -1  =>  count = 1 + (-1) = 0
	 *   Then the new session's +1 is included: actually make returns
	 *   the net change including the new +1.
	 */
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	/* Session A: ts=0, timeout=32 -> expires at tick 2 */
	active_sessions_tracker_new_session(tracker, 0, 0, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count, 1, "count=1 after session A"
	);

	/*
	 * Session B at ts=32 (tick=2): expiry of A fires, new B starts.
	 * Net change = 0 (expiry -1 + new start +1).
	 * count stays 1.
	 */
	active_sessions_tracker_new_session(tracker, 0, 32, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		1,
		"count=1: A expired(-1) + B started(+1) = net 0"
	);

	/*
	 * Advance to ts=64 (tick=4): expiry of B fires.
	 * Session C at ts=64, timeout=32 -> until_tick=(64+32+15)/16=6
	 * make(4,6):
	 *   diff[4%8=4] += 1  -> diff[4] = -1+1 = 0
	 *   diff[6%8=6] -= 1
	 *   advance(4): sweep [2..4): slot 2 (=0), slot 3 (=0);
	 *               consume slot 4 (=0) -> 0
	 *   return 0
	 * count stays 1.
	 */
	active_sessions_tracker_new_session(tracker, 0, 64, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		1,
		"count=1: B expired(-1) + C started(+1) = net 0"
	);

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 4: prolong_session does not change count at current time       */
/* ------------------------------------------------------------------ */
static int
test_prolong_session_does_not_change_count(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 1;
	/*
	 * Session at ts=0, timeout=32:
	 *   now_tick=0, until_tick=2
	 *   count=1
	 *
	 * Prolong at ts=0: prev_timeout=32, new_timeout=64
	 *   prev_until_tick = (0+32+15)/16 = 2
	 *   new_until_tick  = (0+64+15)/16 = 4
	 *   prolong(now_tick=0, prev_until=2, new_until=4):
	 *     diff[2]+=1 -> 0 (cancel old expiry)
	 *     diff[4]-=1
	 *     advance(0): consume slot 0 (=0) -> 0
	 *     return 0
	 *   count += 0  =>  count stays 1
	 */
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	active_sessions_tracker_new_session(tracker, 0, 0, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count, 1, "count=1 after new_session"
	);

	active_sessions_tracker_prolong_session(tracker, 0, 0, 32, 0, 64);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		1,
		"prolong at same timestamp should not change count"
	);

	/*
	 * Advance to ts=32 (tick=2): old expiry was moved to tick=4,
	 * so no expiry fires here.
	 * Session B at ts=32, timeout=32 -> until_tick=4.
	 * make(2, 4):
	 *   diff[2]+=1 (=0+1=1); diff[4]-=1 (=-1-1=-2)
	 *   advance(2): sweep [0..2): slot 0 (=0), slot 1 (=0);
	 *               consume slot 2 (=1) -> +1
	 *   return 0 + 1 = 1
	 * count += 1  =>  count = 2
	 *
	 * No expiry at tick=2 (prolong moved it to tick=4).
	 */
	active_sessions_tracker_new_session(tracker, 0, 32, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].count,
		2,
		"no expiry at old until=tick2; new session starts -> count=2"
	);

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 5: multiple shards are independent                             */
/* ------------------------------------------------------------------ */
static int
test_multiple_shards_are_independent(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 4;
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	/* Add different numbers of sessions to each shard. */
	for (uint32_t w = 0; w < (uint32_t)shards; ++w) {
		for (uint32_t s = 0; s <= w; ++s) {
			active_sessions_tracker_new_session(
				tracker, w, s * 16, 32
			);
		}
	}

	/*
	 * Expected counts with timeout=32 and spacing=16:
	 * each session lasts 2 tracker ticks, and we start one every tick,
	 * so at most 2 sessions overlap on a shard.
	 *
	 *   shard 0: 1 session  (ts=0)
	 *   shard 1: 2 sessions (ts=0,16)
	 *   shard 2: 2 sessions (ts=0 expired when ts=32 started)
	 *   shard 3: 2 sessions (steady-state overlap of 2)
	 */
	static const uint32_t expected[] = {1, 2, 2, 2};
	for (uint32_t w = 0; w < (uint32_t)shards; ++w) {
		TEST_ASSERT_EQUAL(
			(int64_t)tracker[w].count,
			(int64_t)expected[w],
			"shard[%u].count should be %u",
			w,
			expected[w]
		);
	}

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 6: last_packet_timestamp is updated by new_session and prolong */
/* ------------------------------------------------------------------ */
static int
test_last_packet_timestamp_is_updated(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 1;
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	/*
	 * new_session at ts=32:
	 *   now_tick=2, until_tick=(32+32+15)/16=4
	 *   last_packet_timestamp = 32
	 */
	active_sessions_tracker_new_session(tracker, 0, 32, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].last_packet_timestamp,
		32,
		"last_packet_timestamp should be 32 after new_session(ts=32)"
	);

	/*
	 * prolong at ts=48 (tick=3):
	 *   prev_until_tick = (32+32+15)/16 = 4  (>= now_tick=3 ✓)
	 *   new_until_tick  = (48+32+15)/16 = 5
	 *   5 - 3 = 2 < 8 ✓
	 *   last_packet_timestamp = 48
	 */
	active_sessions_tracker_prolong_session(tracker, 0, 32, 32, 48, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].last_packet_timestamp,
		48,
		"last_packet_timestamp should be 48 after prolong(now=48)"
	);

	/*
	 * new_session at ts=80 (tick=5):
	 *   now_tick=5, until_tick=(80+32+15)/16=7
	 *   last_packet_timestamp = 80
	 */
	active_sessions_tracker_new_session(tracker, 0, 80, 32);
	TEST_ASSERT_EQUAL(
		(int64_t)tracker[0].last_packet_timestamp,
		80,
		"last_packet_timestamp should be 80 after new_session(ts=80)"
	);

	active_sessions_tracker_destroy(tracker, shards, &mctx);
	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 7: destroy frees memory (balloc_count == bfree_count)          */
/* ------------------------------------------------------------------ */
static int
test_destroy_frees_memory(void) {
	struct block_allocator ba;
	struct memory_context mctx;
	setup_mctx(&ba, &mctx);

	const size_t shards = 2;
	struct active_sessions_tracker_shard *tracker =
		active_sessions_tracker_create(&mctx, shards, 0);
	TEST_ASSERT_NOT_NULL(tracker, "tracker_create should not return NULL");

	TEST_ASSERT_EQUAL(
		(int64_t)mctx.balloc_count,
		1,
		"exactly one allocation should have been made"
	);
	TEST_ASSERT_EQUAL(
		(int64_t)mctx.bfree_count, 0, "no frees before destroy"
	);

	active_sessions_tracker_destroy(tracker, shards, &mctx);

	TEST_ASSERT_EQUAL(
		(int64_t)mctx.bfree_count, 1, "exactly one free after destroy"
	);
	TEST_ASSERT_EQUAL(
		(int64_t)mctx.balloc_count,
		(int64_t)mctx.bfree_count,
		"balloc_count should equal bfree_count after destroy"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	LOG(INFO, "test_create_initialises_shards...");
	TEST_ASSERT_SUCCESS(
		test_create_initialises_shards(),
		"test_create_initialises_shards failed"
	);

	LOG(INFO, "test_new_session_increments_count...");
	TEST_ASSERT_SUCCESS(
		test_new_session_increments_count(),
		"test_new_session_increments_count failed"
	);

	LOG(INFO, "test_session_expires_decrements_count...");
	TEST_ASSERT_SUCCESS(
		test_session_expires_decrements_count(),
		"test_session_expires_decrements_count failed"
	);

	LOG(INFO, "test_prolong_session_does_not_change_count...");
	TEST_ASSERT_SUCCESS(
		test_prolong_session_does_not_change_count(),
		"test_prolong_session_does_not_change_count failed"
	);

	LOG(INFO, "test_multiple_shards_are_independent...");
	TEST_ASSERT_SUCCESS(
		test_multiple_shards_are_independent(),
		"test_multiple_shards_are_independent failed"
	);

	LOG(INFO, "test_last_packet_timestamp_is_updated...");
	TEST_ASSERT_SUCCESS(
		test_last_packet_timestamp_is_updated(),
		"test_last_packet_timestamp_is_updated failed"
	);

	LOG(INFO, "test_destroy_frees_memory...");
	TEST_ASSERT_SUCCESS(
		test_destroy_frees_memory(), "test_destroy_frees_memory failed"
	);

	return TEST_SUCCESS;
}
