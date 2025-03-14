#pragma once

#include <stdint.h>
#include <stdio.h>
#include <strings.h>
#include <time.h>
#include <unistd.h>

#define LOG_RED "\x1b[31m"
#define LOG_GREEN "\x1b[32m"
#define LOG_YELLOW "\x1b[33m"
#define LOG_BLUE "\x1b[34m"
#define LOG_MAGENTA "\x1b[35m"
#define LOG_CYAN "\x1b[36m"
#define LOG_GRAY "\x1b[02;39m"
#define LOG_RESET "\x1b[0m"

static char *__log_color_reset = LOG_RESET; // NOLINT

/*
 * List of log-ids.
 */
enum log_id { TRACE, DEBUG, INFO, WARN, ERROR, LOG_ID_MAX }; // NOLINT

struct logger {
	uint8_t enable;
	char *name;
	char *color;
};

static struct logger loggers[LOG_ID_MAX] = {
	[TRACE] = {.name = "TRACE", .color = LOG_CYAN},
	[DEBUG] = {.name = "DEBUG", .color = LOG_GRAY},
	[INFO] = {.name = "INFO", .color = LOG_BLUE},
	[WARN] = {.name = "WARN", .color = LOG_YELLOW},
	[ERROR] = {.name = "ERROR", .color = LOG_RED},
};

#define LOG(log_level, fmt_, ...)                                              \
	do {                                                                   \
		if (log_enabled(log_level)) {                                  \
			fprintf(stderr,                                        \
				"%s [%s%-6s%s][%s:%d]: " fmt_ "\n",            \
				log_fmt_timestamp(),                           \
				log_color(log_level),                          \
				log_name(log_level),                           \
				log_color_reset(),                             \
				__FILE_NAME__,                                 \
				__LINE__,                                      \
				##__VA_ARGS__);                                \
		}                                                              \
	} while (0)

static inline const char *
log_fmt_timestamp(void) {
	static char ts_str[sizeof("2025-03-14T17:57:21.777541")];
	struct timespec now;
	struct tm tm;
	int len;

	clock_gettime(CLOCK_REALTIME, &now);
	localtime_r(&now.tv_sec, &tm);

	len = strftime(ts_str, sizeof(ts_str), "%FT%T", &tm);
	snprintf(
		ts_str + len, sizeof(ts_str) - len, ".%06lu", now.tv_nsec / 1000
	);

	return ts_str;
}

static inline const char *
log_name(enum log_id lid) {
	return loggers[lid].name;
}

static inline const char *
log_color(enum log_id lid) {
	return loggers[lid].color;
}

static inline const char *
log_color_reset(void) {
	return __log_color_reset;
}

static inline uint8_t
log_enabled(enum log_id lid) {
	return loggers[lid].enable;
}

static inline void
log_enable_id(enum log_id lid) {
	loggers[lid].enable = 1;
}
static inline void
log_disable_id(enum log_id lid) {
	loggers[lid].enable = 0;
}

static inline void
log_reset(void) {
	for (uint64_t idx = 0; idx < sizeof(loggers) / sizeof(struct logger);
	     idx++) {
		loggers[idx].enable = 0;
	}
}
static inline void
log_enable_name(char *log_name) {
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
