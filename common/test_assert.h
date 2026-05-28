#pragma once

#include "lib/logging/log.h"

#include <string.h>

/* Test framework macros */
#define TEST_SUCCESS 0
#define TEST_FAILED -1

#define TEST_ASSERT(cond, msg, ...)                                            \
	do {                                                                   \
		if (!(cond)) {                                                 \
			LOG(ERROR, "ASSERT FAILED: " msg, ##__VA_ARGS__);      \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)

#define TEST_ASSERT_SUCCESS(value, msg, ...)                                   \
	do {                                                                   \
		if ((value) != TEST_SUCCESS) {                                 \
			LOG(ERROR, "ASSERT FAILED: " msg, ##__VA_ARGS__);      \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)

#define TEST_ASSERT_EQUAL(a, b, msg, ...)                                      \
	do {                                                                   \
		if ((a) != (b)) {                                              \
			LOG(ERROR,                                             \
			    "ASSERT FAILED: " msg                              \
			    " (expected: %ld, got: %ld)",                      \
			    ##__VA_ARGS__,                                     \
			    (long)(b),                                         \
			    (long)(a));                                        \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)

#define TEST_ASSERT_STR_CONTAINS(haystack, needle, msg, ...)                   \
	do {                                                                   \
		const char *_hay = (haystack);                                 \
		const char *_ndl = (needle);                                   \
		if (_hay == NULL || _ndl == NULL ||                            \
		    strstr(_hay, _ndl) == NULL) {                              \
			LOG(ERROR,                                             \
			    "ASSERT FAILED: " msg                              \
			    " (expected substring '%s' in '%s')",              \
			    ##__VA_ARGS__,                                     \
			    _ndl ? _ndl : "(null)",                            \
			    _hay ? _hay : "(null)");                           \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)

#define TEST_ASSERT_NOT_NULL(ptr, msg, ...)                                    \
	do {                                                                   \
		if ((ptr) == NULL) {                                           \
			LOG(ERROR, "ASSERT FAILED: " msg, ##__VA_ARGS__);      \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)

#define TEST_ASSERT_NULL(ptr, msg, ...)                                        \
	do {                                                                   \
		if ((ptr) != NULL) {                                           \
			LOG(ERROR, "ASSERT FAILED: " msg, ##__VA_ARGS__);      \
			return TEST_FAILED;                                    \
		}                                                              \
	} while (0)
