/*
 * Unit tests for rt_interval_counter.
 *
 * Ring size = 8 (RT_INTERVAL_COUNTER_RING_SIZE), mask = 7.
 *
 * The counter stores int32_t diff[8].  make(now, until) writes +1 at
 * now%8 and -1 at until%8, then advances past `now` (consuming and
 * clearing that slot).  The return value is the net change to the
 * caller's running total.
 *
 * prolong(now, prev_until, new_until) moves the -1 from prev_until%8
 * to new_until%8 by writing +1 at prev_until%8 and -1 at new_until%8,
 * then advances past `now`.  It does NOT change the count at `now`.
 *
 * When now - last_timestamp >= 8 (stale gap), try_reset() sums all
 * remaining diffs, clears the ring, and resets last_timestamp = now.
 * That sum is included in the return value of the next make/prolong.
 */

#include "../../controlplane/state/interval_counter.h"
#include "../../../../common/test_assert.h"
#include "../../dataplane/interval_counter.h"
#include "lib/logging/log.h"
#include <stdint.h>

/* ------------------------------------------------------------------ */
/* Test 1: each make() at a new timestamp returns +1                   */
/* ------------------------------------------------------------------ */
static int
test_make_reports_visible_count_changes(void) {
	struct rt_interval_counter counter;

	rt_interval_counter_init(&counter, 10);

	/*
	 * make(10, 13):
	 *   diff[10%8=2] += 1  -> diff[2] = 1
	 *   diff[13%8=5] -= 1  -> diff[5] = -1
	 *   advance(10): consume slot 2 -> change = +1, diff[2] = 0
	 *   return 0 + 1 = 1
	 */
	int64_t change = rt_interval_counter_make(&counter, 10, 13);
	TEST_ASSERT_EQUAL(change, 1, "make(10,13) should return +1");

	/*
	 * make(11, 15):
	 *   diff[11%8=3] += 1  -> diff[3] = 1
	 *   diff[15%8=7] -= 1  -> diff[7] = -1
	 *   advance(11): sweep slot 10%8=2 (=0) -> change=0;
	 *                consume slot 11%8=3 (=1) -> change=1, diff[3]=0
	 *   return 0 + 1 = 1
	 */
	change = rt_interval_counter_make(&counter, 11, 15);
	TEST_ASSERT_EQUAL(change, 1, "make(11,15) should return +1");

	/*
	 * make(12, 16):
	 *   diff[12%8=4] += 1
	 *   diff[16%8=0] -= 1
	 *   advance(12): sweep slot 11%8=3 (=0); consume slot 12%8=4 (=1)
	 *   return 1
	 */
	change = rt_interval_counter_make(&counter, 12, 16);
	TEST_ASSERT_EQUAL(change, 1, "make(12,16) should return +1");

	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 2: prolong() returns 0; expiry fires at new time, not old      */
/* ------------------------------------------------------------------ */
static int
test_prolong_moves_expiry_without_changing_current_count(void) {
	struct rt_interval_counter counter;

	rt_interval_counter_init(&counter, 20);

	/*
	 * make(20, 23):
	 *   diff[20%8=4] += 1; diff[23%8=7] -= 1
	 *   advance(20): consume slot 4 -> +1
	 *   return 1
	 */
	int64_t change = rt_interval_counter_make(&counter, 20, 23);
	TEST_ASSERT_EQUAL(change, 1, "make(20,23) should return +1");

	/*
	 * prolong(20, prev_until=23, new_until=26):
	 *   diff[23%8=7] += 1  -> diff[7] = -1+1 = 0  (cancel old expiry)
	 *   diff[26%8=2] -= 1  -> diff[2] = -1          (new expiry)
	 *   advance(20): consume slot 20%8=4 (=0) -> change=0
	 *   return 0 + 0 = 0
	 */
	change = rt_interval_counter_prolong(&counter, 20, 23, 26);
	TEST_ASSERT_EQUAL(change, 0, "prolong(20,23,26) should return 0");

	/*
	 * make(23, 27): a new interval starts at t=23.
	 *   diff[23%8=7] += 1  -> diff[7] = 0+1 = 1  (slot was cleared by
	 * prolong) diff[27%8=3] -= 1  -> diff[3] = -1 advance(23): sweep
	 * [20..23): slot 20%8=4 (=0), slot 21%8=5 (=0), slot 22%8=6 (=0)
	 *   consume slot 23%8=7 (=1) -> change=+1, diff[7]=0
	 *   return 0 + 1 = 1
	 *
	 * The prolong cancelled the -1 expiry at slot 7, so there is no
	 * expiry firing here.  The +1 is purely from the new interval start.
	 */
	change = rt_interval_counter_make(&counter, 23, 27);
	TEST_ASSERT_EQUAL(
		change,
		1,
		"make(23,27): new interval starts (+1), no expiry at old "
		"until=23"
	);

	/*
	 * make(26, 30): the prolong moved the expiry to slot 26%8=2.
	 *   diff[26%8=2] += 1  -> diff[2] = -1+1 = 0  (cancels prolong expiry)
	 *   diff[30%8=6] -= 1  -> diff[6] = -1
	 *   advance(26): sweep [23..26):
	 *     slot 23%8=7 (=0), slot 24%8=0 (=0), slot 25%8=1 (=0)
	 *   consume slot 26%8=2 (=0) -> change=0
	 *   return 0 + 0 = 0
	 *
	 * The +1 from make(26,30) cancels the prolong's -1 at slot 2.
	 * Net = 0: the prolonged interval expired (-1) and a new one started
	 * (+1).
	 */
	change = rt_interval_counter_make(&counter, 26, 30);
	TEST_ASSERT_EQUAL(
		change, 0, "at t=26: prolong expiry(-1) + new interval(+1) = 0"
	);

	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 3: multiple make() at the same now each return +1              */
/* ------------------------------------------------------------------ */
static int
test_same_timestamp_operations_accumulate_once(void) {
	struct rt_interval_counter counter;

	rt_interval_counter_init(&counter, 30);

	/*
	 * Three intervals all starting at now=30.
	 * Each make() writes +1 at slot 30%8=6, then advance() consumes
	 * and clears slot 6 immediately, so each call returns +1.
	 * The -1 expiry slots accumulate independently at slots 3, 4, 5.
	 *
	 *   35%8=3, 36%8=4, 37%8=5
	 */
	int64_t change = rt_interval_counter_make(&counter, 30, 35);
	TEST_ASSERT_EQUAL(change, 1, "1st make(30,35) should return +1");

	change = rt_interval_counter_make(&counter, 30, 36);
	TEST_ASSERT_EQUAL(change, 1, "2nd make(30,36) should return +1");

	change = rt_interval_counter_make(&counter, 30, 37);
	TEST_ASSERT_EQUAL(change, 1, "3rd make(30,37) should return +1");

	/*
	 * State: diff[3]=-1, diff[4]=-1, diff[5]=-1, last_timestamp=30.
	 *
	 * Jump far ahead (stale gap >= 8) to flush all three pending
	 * expiries at once via try_reset, then start a new interval.
	 *
	 * make(200, 202): gap=200-30=170 >= 8 -> try_reset fires.
	 *   try_reset: sum all diffs: diff[3]+diff[4]+diff[5] = -3
	 *              clear ring; last_timestamp=200; return -3
	 *   diff[200%8=0] += 1; diff[202%8=2] -= 1
	 *   advance(200): consume slot 0 (=1) -> +1
	 *   return -3 + 1 = -2
	 *
	 * Net -2: three intervals expired (-3) and one new started (+1).
	 */
	change = rt_interval_counter_make(&counter, 200, 202);
	TEST_ASSERT_EQUAL(
		change,
		-2,
		"stale gap flushes 3 pending expiries(-3) + new interval(+1) = "
		"-2"
	);

	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 4: stale gap flushes pending diffs before the new interval     */
/* ------------------------------------------------------------------ */
static int
test_stale_gap_returns_pending_sum_before_new_interval(void) {
	struct rt_interval_counter counter;
	const uint32_t now = 100;

	rt_interval_counter_init(&counter, now);

	/*
	 * make(100, 103):
	 *   diff[100%8=4] += 1; diff[103%8=7] -= 1
	 *   advance(100): consume slot 4 -> +1
	 *   return 1
	 */
	int64_t change = rt_interval_counter_make(&counter, 100, 103);
	TEST_ASSERT_EQUAL(change, 1, "make(100,103) should return +1");

	/*
	 * make(200, 205): gap = 200-100 = 100 >= 8 -> try_reset fires.
	 *   try_reset: sum all diffs: diff[7]=-1, rest 0 -> sum=-1
	 *              clear ring; last_timestamp=200; return -1
	 *   diff[200%8=0] += 1; diff[205%8=5] -= 1
	 *   advance(200): consume slot 0 (=1) -> +1
	 *   return -1 + 1 = 0
	 *
	 * Net 0: the pending expiry (-1) and the new interval (+1) cancel.
	 */
	change = rt_interval_counter_make(&counter, 200, 205);
	TEST_ASSERT_EQUAL(
		change,
		0,
		"stale gap: pending expiry(-1) + new interval(+1) = 0"
	);

	/*
	 * make(201, 206): no stale gap (201-200=1 < 8).
	 *   diff[201%8=1] += 1; diff[206%8=6] -= 1
	 *   advance(201): sweep slot 200%8=0 (=0); consume slot 1 (=1) -> +1
	 *   return 0 + 1 = 1
	 */
	change = rt_interval_counter_make(&counter, 201, 206);
	TEST_ASSERT_EQUAL(
		change, 1, "make(201,206) after reset should return +1"
	);

	/*
	 * Another large jump: make(400, 405).
	 *   gap = 400-201 = 199 >= 8 -> try_reset fires.
	 *   Remaining diffs: diff[205%8=5]=-1, diff[206%8=6]=-1, rest 0
	 *   sum = -2; clear; last_timestamp=400
	 *   diff[400%8=0] += 1; diff[405%8=5] -= 1
	 *   advance(400): consume slot 0 (=1) -> +1
	 *   return -2 + 1 = -1
	 */
	change = rt_interval_counter_make(&counter, 400, 405);
	TEST_ASSERT_EQUAL(
		change,
		-1,
		"second stale gap: two pending expiries(-2) + new interval(+1) "
		"= -1"
	);

	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 5: init zeroes the ring and sets last_timestamp correctly      */
/* ------------------------------------------------------------------ */
static int
test_init_zeroes_ring_and_sets_timestamp(void) {
	struct rt_interval_counter counter;

	/* Dirty the memory first to ensure init actually zeroes it. */
	for (size_t i = 0; i < RT_INTERVAL_COUNTER_RING_SIZE; ++i) {
		counter.diff[i] = (int32_t)(i + 1);
	}
	counter.last_timestamp = 0xDEADBEEFu;

	rt_interval_counter_init(&counter, 42);

	TEST_ASSERT_EQUAL(
		(int64_t)counter.last_timestamp,
		42,
		"last_timestamp should be set to now=42"
	);

	for (size_t i = 0; i < RT_INTERVAL_COUNTER_RING_SIZE; ++i) {
		TEST_ASSERT_EQUAL(
			(int64_t)counter.diff[i],
			0,
			"diff[%zu] should be zeroed after init",
			i
		);
	}

	return TEST_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* Test 6: expiry fires at the exact timestamp, not before or after    */
/* ------------------------------------------------------------------ */
static int
test_expiry_fires_at_correct_timestamp(void) {
	struct rt_interval_counter counter;

	/*
	 * Constraint: until - now < 8 (ring size).
	 * Use short intervals of length 3 and 4.
	 *
	 * Interval A: [10, 13)  diff[10%8=2]+=1 consumed, diff[13%8=5]-=1
	 * Interval B: [11, 14)  diff[11%8=3]+=1 consumed, diff[14%8=6]-=1
	 */
	rt_interval_counter_init(&counter, 10);

	int64_t change = rt_interval_counter_make(&counter, 10, 13);
	TEST_ASSERT_EQUAL(change, 1, "make(10,13) should return +1");

	change = rt_interval_counter_make(&counter, 11, 14);
	TEST_ASSERT_EQUAL(change, 1, "make(11,14) should return +1");

	/*
	 * Advance to t=12 (before either expiry) via make(12, 15):
	 *   15%8=7, safe (not 5 or 6).
	 *   diff[12%8=4]+=1; diff[15%8=7]-=1
	 *   advance(12): sweep slot 11%8=3 (=0); consume slot 12%8=4 (=1) -> +1
	 *   return 0 + 1 = 1
	 * A new interval starts; no expiry yet.
	 */
	change = rt_interval_counter_make(&counter, 12, 15);
	TEST_ASSERT_EQUAL(
		change, 1, "make(12,15): new interval, no expiry yet"
	);

	/*
	 * Advance to t=13 (expiry of A) via make(13, 16):
	 *   16%8=0, safe (not 5,6,7).
	 *   diff[13%8=5]+=1 -> diff[5]=-1+1=0  (cancel A's expiry)
	 *   diff[16%8=0]-=1
	 *   advance(13): sweep slot 12%8=4 (=0); consume slot 13%8=5 (=0) -> 0
	 *   return 0
	 * Net = 0: A expired (-1) and new interval started (+1).
	 */
	change = rt_interval_counter_make(&counter, 13, 16);
	TEST_ASSERT_EQUAL(
		change, 0, "at t=13: expiry of A(-1) + new start(+1) = 0"
	);

	/*
	 * Advance to t=14 (expiry of B) via make(14, 17):
	 *   17%8=1, safe (not 6,7,0).
	 *   diff[14%8=6]+=1 -> diff[6]=-1+1=0  (cancel B's expiry)
	 *   diff[17%8=1]-=1
	 *   advance(14): sweep slot 13%8=5 (=0); consume slot 14%8=6 (=0) -> 0
	 *   return 0
	 * Net = 0: B expired (-1) and new interval started (+1).
	 */
	change = rt_interval_counter_make(&counter, 14, 17);
	TEST_ASSERT_EQUAL(
		change, 0, "at t=14: expiry of B(-1) + new start(+1) = 0"
	);

	/*
	 * Advance to t=15 (expiry of make(12,15)) via make(15, 18):
	 *   18%8=2, safe (not 7,1).
	 *   diff[15%8=7]+=1 -> diff[7]=-1+1=0  (cancel make(12,15) expiry)
	 *   diff[18%8=2]-=1
	 *   advance(15): sweep slot 14%8=6 (=0); consume slot 15%8=7 (=0) -> 0
	 *   return 0
	 */
	change = rt_interval_counter_make(&counter, 15, 18);
	TEST_ASSERT_EQUAL(
		change,
		0,
		"at t=15: expiry of make(12,15) (-1) + new start(+1) = 0"
	);

	/*
	 * Verify no spurious expiry between t=13 and t=14:
	 * Use a fresh counter and advance from t=12 to t=13 without
	 * starting a new interval — observe only the expiry.
	 *
	 * Since make() always starts a new interval, we can't call it
	 * without the +1 side effect.  Instead, verify by checking that
	 * make(13, 16) returns 0 (not -1), confirming the expiry and
	 * start cancel exactly.  This was already asserted above.
	 */

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	LOG(INFO, "test_make_reports_visible_count_changes...");
	TEST_ASSERT_SUCCESS(
		test_make_reports_visible_count_changes(),
		"test_make_reports_visible_count_changes failed"
	);

	LOG(INFO,
	    "test_prolong_moves_expiry_without_changing_current_count...");
	TEST_ASSERT_SUCCESS(
		test_prolong_moves_expiry_without_changing_current_count(),
		"test_prolong_moves_expiry_without_changing_current_count "
		"failed"
	);

	LOG(INFO, "test_same_timestamp_operations_accumulate_once...");
	TEST_ASSERT_SUCCESS(
		test_same_timestamp_operations_accumulate_once(),
		"test_same_timestamp_operations_accumulate_once failed"
	);

	LOG(INFO, "test_stale_gap_returns_pending_sum_before_new_interval...");
	TEST_ASSERT_SUCCESS(
		test_stale_gap_returns_pending_sum_before_new_interval(),
		"test_stale_gap_returns_pending_sum_before_new_interval failed"
	);

	LOG(INFO, "test_init_zeroes_ring_and_sets_timestamp...");
	TEST_ASSERT_SUCCESS(
		test_init_zeroes_ring_and_sets_timestamp(),
		"test_init_zeroes_ring_and_sets_timestamp failed"
	);

	LOG(INFO, "test_expiry_fires_at_correct_timestamp...");
	TEST_ASSERT_SUCCESS(
		test_expiry_fires_at_correct_timestamp(),
		"test_expiry_fires_at_correct_timestamp failed"
	);

	return TEST_SUCCESS;
}
