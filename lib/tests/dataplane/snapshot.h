#pragma once

#include <stdint.h>
#include <string.h>

#include "common/test_assert.h"

#include "dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

/*
 * Capture every byte of a packet's mbuf data area along with pkt_len and
 * data_off so a test can later assert the packet was not modified by a
 * function that was supposed to be a no-op (or that returned an error
 * without touching the packet).
 */

#define PKT_SNAPSHOT_CAP 512

struct pkt_snapshot {
	uint16_t pkt_len;
	uint16_t data_off;
	uint8_t data[PKT_SNAPSHOT_CAP];
};

static inline void
snapshot(struct packet *p, struct pkt_snapshot *s) {
	uint16_t len = packet_data_len(p);
	s->pkt_len = len;
	s->data_off = p->mbuf->data_off;
	memcpy(s->data, packet_data(p), len);
}

static inline int
assert_unchanged(struct packet *p, const struct pkt_snapshot *s) {
	TEST_ASSERT_EQUAL(packet_data_len(p), s->pkt_len, "pkt_len changed");
	TEST_ASSERT_EQUAL(
		packet_data_offset(p), s->data_off, "data_off changed"
	);
	TEST_ASSERT(
		memcmp(packet_data(p), s->data, s->pkt_len) == 0,
		"packet bytes changed"
	);
	return TEST_SUCCESS;
}
