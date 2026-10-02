#pragma once

#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

// The meson-generated header is absent for cgo compilation of Go packages
// reaching this file, which runs without a configured build directory.
// Only ENABLE_TRACE_LOG is consumed below, so its absence just leaves trace
// logging disabled, matching a default meson configuration.
#if defined(__has_include)
#if __has_include("yanet_build_config.h")
#include "yanet_build_config.h"
#endif
#else
#include "yanet_build_config.h"
#endif

#define LOG_RED "\x1b[31m"
#define LOG_GREEN "\x1b[32m"
#define LOG_YELLOW "\x1b[33m"
#define LOG_BLUE "\x1b[34m"
#define LOG_MAGENTA "\x1b[35m"
#define LOG_CYAN "\x1b[36m"
#define LOG_GRAY "\x1b[02;39m"
#define LOG_RESET "\x1b[0m"

// Hack around gcc versions < 12, which don't have `__FILE_NAME__` macro
// defined.
#ifndef __FILE_NAME__
#include <string.h>
static inline const char *
__yanet_path_basename(const char *path) {
	const char *base = strrchr(path, '/');
	return base ? base + 1 : path;
}

#define __FILE_NAME__ __yanet_path_basename(__FILE__)
#endif

/*
 * List of log-ids.
 */
enum log_id { TRACE, DEBUG, INFO, WARN, ERROR, LOG_ID_MAX }; // NOLINT

#define LOG(log_level, fmt_, ...)                                              \
	do {                                                                   \
		if (log_enabled(log_level)) {                                  \
			log_write(                                             \
				log_level,                                     \
				__FILE_NAME__,                                 \
				__LINE__,                                      \
				fmt_,                                          \
				##__VA_ARGS__                                  \
			);                                                     \
		}                                                              \
	} while (0)

// Upper bound of the coarse monotonic clock step: one jiffy at HZ=100.
#define LOG_RATE_CLOCK_TICK_NS 10000000LL
#define LOG_RATE_NS_PER_SEC 1000000000LL

// Time one event takes from the bucket, in nanoseconds.
#define LOG_RATE_EVENT_COST(rate) (LOG_RATE_NS_PER_SEC / (rate))

// Smallest burst that still lets the rate through a coarse clock.
//
// Every caller within one clock step sees the same time, so the bucket must
// admit a whole step worth of events at once, and never less than one.
#define LOG_RATE_MIN_BURST(rate)                                               \
	((LOG_RATE_CLOCK_TICK_NS + LOG_RATE_EVENT_COST(rate) - 1) /            \
	 LOG_RATE_EVENT_COST(rate))

// Token bucket shared by every thread reaching one rate-limited log site.
//
// The bucket keeps the earliest departure time of the next event: an event
// advances it by its cost and passes while the advance stays within the burst
// window ahead of now. Only that time is mutable, so the bucket needs no
// runtime initialisation.
struct log_rate_bucket {
	int64_t edt;
	int64_t cost;
	int64_t cap;
};

#define LOG_RATE_BUCKET_INIT(rate)                                             \
	{                                                                      \
		.edt = 0,                                                      \
		.cost = LOG_RATE_EVENT_COST(rate),                             \
		.cap = LOG_RATE_EVENT_COST(rate) * LOG_RATE_MIN_BURST(rate),   \
	}

// Takes one event from the bucket at the given monotonic time in nanoseconds.
//
// An idle bucket restarts its schedule from now, so a pause never saves up
// more than the burst; callers racing on that restart all pass. A busy bucket
// rejects without touching the schedule once the burst window is full.
static inline bool
log_rate_consume(struct log_rate_bucket *bucket, int64_t now) {
	int64_t edt = __atomic_load_n(&bucket->edt, __ATOMIC_RELAXED);
	int64_t usage = edt - now;
	if (usage < 0) {
		__atomic_compare_exchange_n(
			&bucket->edt,
			&edt,
			now + bucket->cost,
			false,
			__ATOMIC_RELAXED,
			__ATOMIC_RELAXED
		);
		return true;
	}
	if (usage > bucket->cap - bucket->cost) {
		return false;
	}
	// The check above raced with other callers, so the slot is claimed
	// first and rechecked against the burst window afterwards.
	usage = __atomic_fetch_add(
			&bucket->edt, bucket->cost, __ATOMIC_RELAXED
		) -
		now;
	return usage <= bucket->cap;
}

static inline bool
log_rate_allow(struct log_rate_bucket *bucket) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC_COARSE, &ts);
	return log_rate_consume(
		bucket, ts.tv_sec * LOG_RATE_NS_PER_SEC + ts.tv_nsec
	);
}

// Logs at most rate messages per second from this call site.
//
// The rate is an integer constant from 1 to 10^9; the bucket is a static
// object of the call site, shared by every thread and sized at compile time.
// A disabled level takes nothing from the bucket. A call site inside a static
// inline function gets one bucket per translation unit, and C forbids one in
// an extern inline function. The format must be a string literal.
//
// Each thread counts its own rejected messages, so a rejection never writes
// memory shared with other threads; the next message the thread passes
// carries that count and resets it.
#define LOG_RATE(log_level, rate, fmt_, ...)                                   \
	do {                                                                   \
		_Static_assert(                                                \
			(rate) >= 1 && (rate) <= LOG_RATE_NS_PER_SEC,          \
			"LOG_RATE rate must be within [1, 10^9] per second"    \
		);                                                             \
		static struct log_rate_bucket __log_rate_bucket =              \
			LOG_RATE_BUCKET_INIT(rate);                            \
		static __thread uint64_t __log_rate_suppressed;                \
		if (!log_enabled(log_level)) {                                 \
			break;                                                 \
		}                                                              \
		if (!log_rate_allow(&__log_rate_bucket)) {                     \
			__log_rate_suppressed++;                               \
			break;                                                 \
		}                                                              \
		if (__log_rate_suppressed == 0) {                              \
			log_write(                                             \
				log_level,                                     \
				__FILE_NAME__,                                 \
				__LINE__,                                      \
				fmt_,                                          \
				##__VA_ARGS__                                  \
			);                                                     \
			break;                                                 \
		}                                                              \
		log_write(                                                     \
			log_level,                                             \
			__FILE_NAME__,                                         \
			__LINE__,                                              \
			fmt_ " [%" PRIu64 " suppressed]",                      \
			##__VA_ARGS__,                                         \
			__log_rate_suppressed                                  \
		);                                                             \
		__log_rate_suppressed = 0;                                     \
	} while (0)

#ifdef ENABLE_TRACE_LOG
#define LOG_TRACE(fmt, ...)                                                    \
	do {                                                                   \
		LOG(TRACE, fmt, ##__VA_ARGS__);                                \
	} while (0)
#define LOG_TRACEX(f, fmt, ...)                                                \
	do {                                                                   \
		f;                                                             \
		LOG(TRACE, fmt, ##__VA_ARGS__);                                \
	} while (0)
#else
#define LOG_TRACE(...) (void)(0)
#define LOG_TRACEX(...) (void)(0)
#endif // ENABLE_TRACE_LOG

// Sink hook: receives the bare formatted message, no timestamp, level name
// or color, so a process embedding this library can route log lines through
// its own logger instead of stderr. The message is truncated beyond 1024
// bytes, unlike the unbounded stderr path.
typedef void (*log_sink_fn)(
	enum log_id level,
	const char *file,
	int line,
	const char *msg,
	void *ctx
);

void
log_set_sink(log_sink_fn sink, void *ctx);

void
log_write(enum log_id level, const char *file, int line, const char *fmt, ...)
	__attribute__((format(printf, 4, 5)));

const char *
log_fmt_timestamp(void);
/**
 * Returns the name of the logger associated with the given log ID.
 *
 * @param lid The log ID for which to retrieve the logger name.
 * @return A pointer to a constant character string representing the logger's
 * name.
 */
const char *
log_name(enum log_id lid);

const char *
log_color(enum log_id lid);

const char *
log_color_reset(void);

uint8_t
log_enabled(enum log_id lid);

/**
 * Enable logging for a specific logger ID (only).
 * @param lid The logger ID for which logging should be enabled.
 */
void
log_enable_id(enum log_id lid);

void
log_disable_id(enum log_id lid);

/**
 * Disables all log levels currently enabled.
 *
 * This function iterates through the array of loggers and sets the 'enable'
 * field of each logger to 0, effectively disabling all logging.
 */
void
log_reset(void);

/**
 * Enable logging for a specified log name.
 *
 * This function searches for the specified log name in the list of loggers
 * and enables it. If the log name is found, it also enables all levels of logs
 * up to and including the level corresponding to the found logger.
 * Additionally, if the standard error output is not a terminal (i.e., is
 * redirected), it disables color logging for all loggers.
 *
 * @param log_name The name of the logger to enable.
 */
void
log_enable_name(const char *log_name);
