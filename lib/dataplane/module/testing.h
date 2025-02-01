#include "module.h"

struct test_data {
	const char *payload;
	uint16_t size;
};

struct packet_front *
testing_packet_front(
	struct test_data payload[], uint64_t count, uint16_t buf_len
);
uint8_t *
testing_packet_data(const struct packet *p, uint16_t *len);
