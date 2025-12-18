#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <strings.h>
#include <time.h>
#include <unistd.h>

#include "log.h"

static const char *__log_color_reset = LOG_RESET; // NOLINT

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

inline uint8_t
log_enabled(enum log_id lid) {
	return loggers[lid].enable;
}

/**
 * Enable logging for a specific logger ID (only).
 * @param lid The logger ID for which logging should be enabled.
 */
inline void
log_enable_id(enum log_id lid) {
	loggers[lid].enable = 1;
}

inline void
log_disable_id(enum log_id lid) {
	loggers[lid].enable = 0;
}

inline void
log_reset(void) {
	for (uint64_t idx = 0; idx < sizeof(loggers) / sizeof(struct logger);
	     idx++) {
		loggers[idx].enable = 0;
	}
}

inline void
log_enable_name(const char *log_name) {
	enum log_id lid = LOG_ID_MAX;
	for (uint64_t idx = 0; idx < sizeof(loggers) / sizeof(struct logger);
	     idx++) {
		if (strcasecmp(loggers[idx].name, log_name) == 0) {
			loggers[idx].enable = 1;
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
		loggers[TRACE].enable = 1;
		// fallthrough
	case DEBUG:
		loggers[DEBUG].enable = 1;
		// fallthrough
	case INFO:
		loggers[INFO].enable = 1;
		// fallthrough
	case WARN:
		loggers[WARN].enable = 1;
		// fallthrough
	case ERROR:
		loggers[ERROR].enable = 1;
		// fallthrough
	default:
		break;
	}
}
