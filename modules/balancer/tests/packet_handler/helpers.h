#pragma once

#define TEST_SUCCESS 0
#define TEST_FAILED -1

#define TEST_ASSERT(cond, msg, ...)                                            \
	do {                                                                   \
		if (!(cond)) {                                                 \
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
