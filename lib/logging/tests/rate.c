// Pins the rate-limited log path: the token bucket and the LOG_RATE macro.
//
// The bucket scenarios drive a synthetic clock, so the expected pass counts
// are exact: a coarse clock step admits the minimal burst, a sustained load
// passes the configured rate, an idle pause saves up no more than the burst,
// and threads hammering one bucket never exceed the burst by more than one
// event per thread. The macro scenario routes messages through the sink.

#include <assert.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

#include "lib/logging/log.h"

#define START_NS (1000 * LOG_RATE_NS_PER_SEC)

// Builds a fresh bucket for the given rate, as a call site would see it.
static struct log_rate_bucket
make_bucket(int64_t rate) {
	struct log_rate_bucket bucket = {
		.edt = 0,
		.cost = LOG_RATE_EVENT_COST(rate),
		.cap = LOG_RATE_EVENT_COST(rate) * LOG_RATE_MIN_BURST(rate),
	};
	return bucket;
}

// Counts passes out of the given number of events at one instant.
static uint64_t
consume_at(struct log_rate_bucket *bucket, int64_t now, uint64_t events) {
	uint64_t passed = 0;
	for (uint64_t idx = 0; idx < events; idx++) {
		passed += log_rate_consume(bucket, now);
	}
	return passed;
}

// Verifies that the minimal burst covers one clock step and never drops
// below one event.
static void
run_min_burst_test(void) {
	assert(LOG_RATE_MIN_BURST(1) == 1);
	assert(LOG_RATE_MIN_BURST(100) == 1);
	assert(LOG_RATE_MIN_BURST(101) == 2);
	assert(LOG_RATE_MIN_BURST(1000) == 10);
	assert(LOG_RATE_MIN_BURST(LOG_RATE_NS_PER_SEC) == LOG_RATE_CLOCK_TICK_NS
	);
}

// Verifies that a fresh bucket passes exactly the minimal burst within one
// clock step.
static void
run_burst_within_clock_step_test(void) {
	struct log_rate_bucket bucket = make_bucket(1000);
	assert(consume_at(&bucket, START_NS, 100) == LOG_RATE_MIN_BURST(1000));
}

// Verifies that a low rate spaces events by their cost.
static void
run_low_rate_spacing_test(void) {
	struct log_rate_bucket bucket = make_bucket(10);
	int64_t cost = LOG_RATE_EVENT_COST(10);

	assert(consume_at(&bucket, START_NS, 5) == 1);
	assert(consume_at(&bucket, START_NS + cost / 2, 5) == 0);
	assert(consume_at(&bucket, START_NS + cost, 5) == 1);
}

// Verifies that a load far above the rate, observed through a coarse clock,
// passes the rate plus at most one burst over a second.
static void
run_sustained_rate_test(void) {
	const int64_t rate = 1000;
	const int64_t step = 4 * 1000 * 1000;
	struct log_rate_bucket bucket = make_bucket(rate);

	uint64_t passed = 0;
	for (int64_t now = START_NS; now < START_NS + LOG_RATE_NS_PER_SEC;
	     now += step) {
		passed += consume_at(&bucket, now, 100);
	}
	assert(passed >= (uint64_t)rate);
	assert(passed <= (uint64_t)(rate + LOG_RATE_MIN_BURST(rate)));
}

// Verifies that a long pause saves up no more than one burst.
static void
run_idle_saves_no_credit_test(void) {
	struct log_rate_bucket bucket = make_bucket(1000);

	assert(consume_at(&bucket, START_NS, 1) == 1);
	assert(consume_at(&bucket, START_NS + 60 * LOG_RATE_NS_PER_SEC, 1000) ==
	       LOG_RATE_MIN_BURST(1000));
}

#define THREAD_COUNT 8

struct hammer_arg {
	struct log_rate_bucket *bucket;
	uint64_t passed;
};

// Fires many events at the shared bucket at one instant.
static void *
hammer(void *data) {
	struct hammer_arg *arg = data;
	arg->passed = consume_at(arg->bucket, START_NS, 100000);
	return NULL;
}

// Verifies that threads sharing a bucket pass at most the burst plus one
// event per thread racing on the idle restart.
static void
run_threads_share_bucket_test(void) {
	struct log_rate_bucket bucket = make_bucket(1000);
	pthread_t threads[THREAD_COUNT];
	struct hammer_arg args[THREAD_COUNT];

	for (int idx = 0; idx < THREAD_COUNT; idx++) {
		args[idx] = (struct hammer_arg){.bucket = &bucket};
		int rc =
			pthread_create(&threads[idx], NULL, hammer, &args[idx]);
		assert(rc == 0);
	}
	uint64_t passed = 0;
	for (int idx = 0; idx < THREAD_COUNT; idx++) {
		pthread_join(threads[idx], NULL);
		passed += args[idx].passed;
	}
	assert(passed >= (uint64_t)LOG_RATE_MIN_BURST(1000));
	assert(passed <= (uint64_t)(LOG_RATE_MIN_BURST(1000) + THREAD_COUNT));
}

static uint64_t sink_messages;
static char sink_last[1024];

// Counts every message delivered to the sink and keeps the last one.
static void
count_sink(
	enum log_id level,
	const char *file,
	int line,
	const char *msg,
	void *ctx
) {
	(void)level;
	(void)file;
	(void)line;
	(void)ctx;
	sink_messages++;
	snprintf(sink_last, sizeof(sink_last), "%s", msg);
}

// One rate-limited call site admitting one message per second.
static void
log_once_per_second(void) {
	LOG_RATE(WARN, 1, "rate limited %d", 1);
}

// Verifies that a disabled level takes nothing from the bucket and an
// enabled one passes a single message per burst.
static void
run_macro_disabled_level_takes_nothing_test(void) {
	log_set_sink(count_sink, NULL);
	log_reset();

	for (int idx = 0; idx < 10; idx++) {
		log_once_per_second();
	}
	assert(sink_messages == 0);

	log_enable_id(WARN);
	for (int idx = 0; idx < 10; idx++) {
		log_once_per_second();
	}
	assert(sink_messages == 1);

	log_reset();
	log_set_sink(NULL, NULL);
}

// One rate-limited call site admitting one message per 10 ms.
static void
log_every_10ms(void) {
	LOG_RATE(WARN, 100, "storm");
}

// Verifies that the next message a thread passes carries the number of
// messages the thread had rejected since its previous one.
static void
run_macro_reports_suppressed_test(void) {
	const struct timespec pause = {.tv_nsec = 20 * 1000 * 1000};

	log_set_sink(count_sink, NULL);
	log_reset();
	log_enable_id(WARN);
	sink_messages = 0;

	for (int idx = 0; idx < 10; idx++) {
		log_every_10ms();
	}
	assert(sink_messages == 1);
	assert(strcmp(sink_last, "storm") == 0);

	nanosleep(&pause, NULL);
	log_every_10ms();
	assert(sink_messages == 2);
	assert(strcmp(sink_last, "storm [9 suppressed]") == 0);

	nanosleep(&pause, NULL);
	log_every_10ms();
	assert(sink_messages == 3);
	assert(strcmp(sink_last, "storm") == 0);

	log_reset();
	log_set_sink(NULL, NULL);
}

int
main(void) {
	run_min_burst_test();
	run_burst_within_clock_step_test();
	run_low_rate_spacing_test();
	run_sustained_rate_test();
	run_idle_saves_no_credit_test();
	run_threads_share_bucket_test();
	run_macro_disabled_level_takes_nothing_test();
	run_macro_reports_suppressed_test();
	printf("all log rate tests passed\n");
	return 0;
}
