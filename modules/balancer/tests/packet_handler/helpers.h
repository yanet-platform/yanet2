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
void
free_packet(struct packet *packet);

struct packet
make_packet(
	uint8_t *src_ip,
	uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags,
	uint16_t vlan
);

struct packet
make_packet_net6(
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port
);