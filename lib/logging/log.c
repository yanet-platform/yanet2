#include <errno.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <strings.h>
#include <time.h>
#include <unistd.h>

#include "log.h"

static const char *__log_color_reset = LOG_RESET; // NOLINT

static log_sink_fn __log_sink = NULL;	  // NOLINT
static void *__log_sink_ctx = NULL;	  // NOLINT
static __thread int __log_sink_depth = 0; // NOLINT

struct logger {
	uint8_t enable;
	const char *name;
	const char *color;
};

static struct logger loggers[LOG_ID_MAX] = {
	[TRACE] = {.name = "TRACE", .color = LOG_CYAN},
	[DEBUG] = {.name = "DEBUG", .color = LOG_GRAY},
	[INFO] = {.name = "INFO", .color = LOG_BLUE},
	[WARN] = {.name = "WARN", .color = LOG_YELLOW},
	[ERROR] = {.name = "ERROR", .color = LOG_RED},
};

const char *
log_fmt_timestamp(void) {
	static char ts_str[sizeof("2025-03-14T17:57:21.777")];
	struct timespec now;
	struct tm tm;
	int len;

	clock_gettime(CLOCK_REALTIME, &now);
	localtime_r(&now.tv_sec, &tm);

	len = strftime(ts_str, sizeof(ts_str), "%FT%T", &tm);
	snprintf(
		ts_str + len,
		sizeof(ts_str) - len,
		".%03lu",
		now.tv_nsec / 1000000
	);

	return ts_str;
}

inline const char *
log_name(enum log_id lid) {
	return loggers[lid].name;
}

inline const char *
log_color(enum log_id lid) {
	return loggers[lid].color;
}

inline const char *
log_color_reset(void) {
	return __log_color_reset;
}

// The control plane can flip a level at runtime while a worker thread
// concurrently reads it through LOG(), so every access to loggers[].enable
// goes through a relaxed atomic: nothing else is ordered against the gate,
// a momentarily stale read is by design, and relaxed still compiles to a
// plain load on the architectures this runs on.
inline uint8_t
log_enabled(enum log_id lid) {
	return __atomic_load_n(&loggers[lid].enable, __ATOMIC_RELAXED);
}

/**
 * Enable logging for a specific logger ID (only).
 * @param lid The logger ID for which logging should be enabled.
 */
inline void
log_enable_id(enum log_id lid) {
	__atomic_store_n(&loggers[lid].enable, 1, __ATOMIC_RELAXED);
}

inline void
log_disable_id(enum log_id lid) {
	__atomic_store_n(&loggers[lid].enable, 0, __ATOMIC_RELAXED);
}

inline void
log_reset(void) {
	for (uint64_t idx = 0; idx < sizeof(loggers) / sizeof(struct logger);
	     idx++) {
		__atomic_store_n(&loggers[idx].enable, 0, __ATOMIC_RELAXED);
	}
}

inline void
log_enable_name(const char *log_name) {
	enum log_id lid = LOG_ID_MAX;
	for (uint64_t idx = 0; idx < sizeof(loggers) / sizeof(struct logger);
	     idx++) {
		if (strcasecmp(loggers[idx].name, log_name) == 0) {
			__atomic_store_n(
				&loggers[idx].enable, 1, __ATOMIC_RELAXED
			);
			lid = (enum log_id)idx;
			break;
		}
	}
	if (!isatty(STDERR_FILENO)) {
		// When stderr is not a terminal, isatty() sets errno to ENOTTY.
		// In cgo context, this causes the error to be non-nil and the
		// false return value is treated as an error condition
		errno = 0;
		// NOTE: disable colors
		for (uint64_t idx = 0;
		     idx < sizeof(loggers) / sizeof(struct logger);
		     idx++) {
			loggers[idx].color = "";
		}
		__log_color_reset = "";
	}
	// enable leveled logs
	switch (lid) {
	case TRACE:
		__atomic_store_n(&loggers[TRACE].enable, 1, __ATOMIC_RELAXED);
		// fallthrough
	case DEBUG:
		__atomic_store_n(&loggers[DEBUG].enable, 1, __ATOMIC_RELAXED);
		// fallthrough
	case INFO:
		__atomic_store_n(&loggers[INFO].enable, 1, __ATOMIC_RELAXED);
		// fallthrough
	case WARN:
		__atomic_store_n(&loggers[WARN].enable, 1, __ATOMIC_RELAXED);
		// fallthrough
	case ERROR:
		__atomic_store_n(&loggers[ERROR].enable, 1, __ATOMIC_RELAXED);
		// fallthrough
	default:
		break;
	}
}

void
log_set_sink(log_sink_fn sink, void *ctx) {
	__log_sink = sink;
	__log_sink_ctx = ctx;
}

void
log_write(enum log_id level, const char *file, int line, const char *fmt, ...) {
	va_list args;
	va_start(args, fmt);

	log_sink_fn sink = __log_sink;
	void *sink_ctx = __log_sink_ctx;

	// A sink calling back into LOG() on the same thread would otherwise
	// recurse forever, so a reentrant call falls back to the stderr path
	// below instead of the sink.
	if (sink != NULL && __log_sink_depth == 0) {
		char msg[1024];
		vsnprintf(msg, sizeof(msg), fmt, args);
		va_end(args);

		__log_sink_depth++;
		sink(level, file, line, msg, sink_ctx);
		__log_sink_depth--;
		return;
	}

	// Locking the stream keeps the three calls below atomic, so a
	// concurrent stderr writer cannot split the prefix from the message.
	flockfile(stderr);
	fprintf(stderr,
		"%s [%s%-5s%s][%s:%d]: ",
		log_fmt_timestamp(),
		log_color(level),
		log_name(level),
		log_color_reset(),
		file,
		line);
	vfprintf(stderr, fmt, args);
	fputc('\n', stderr);
	funlockfile(stderr);
	va_end(args);
}
