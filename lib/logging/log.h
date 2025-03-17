#pragma once

#include <stdint.h>
#include <strings.h>
#include <unistd.h>

#define LOG_RED "\x1b[31m"
#define LOG_GREEN "\x1b[32m"
#define LOG_YELLOW "\x1b[33m"
#define LOG_BLUE "\x1b[34m"
#define LOG_MAGENTA "\x1b[35m"
#define LOG_CYAN "\x1b[36m"
#define LOG_GRAY "\x1b[02;39m"
#define LOG_RESET "\x1b[0m"

/*
 * List of log-ids.
 */
enum log_id { TRACE, DEBUG, INFO, WARN, ERROR, LOG_ID_MAX }; // NOLINT

#define LOG(log_level, fmt_, ...)                                              \
	do {                                                                   \
		if (log_enabled(log_level)) {                                  \
			fprintf(stderr,                                        \
				"%s [%s%-5s%s][%s:%d]: " fmt_ "\n",            \
				log_fmt_timestamp(),                           \
				log_color(log_level),                          \
				log_name(log_level),                           \
				log_color_reset(),                             \
				__FILE_NAME__,                                 \
				__LINE__,                                      \
				##__VA_ARGS__);                                \
		}                                                              \
	} while (0)

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
log_enable_name(char *log_name);
