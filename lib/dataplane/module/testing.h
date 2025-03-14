#include "module.h"

struct test_data {
	const uint8_t *payload;
	uint16_t size;
};

struct packet_front *
testing_packet_front(
	struct test_data payload[],
	uint8_t *arena,
	uint64_t arena_size,
	uint64_t mbuf_count,
	uint16_t mbuf_size
);

uint8_t *
testing_packet_data(const struct packet *p, uint16_t *len);
