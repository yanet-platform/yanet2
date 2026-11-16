#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/rx_service.h"

#include "assert.h"

#include <stdint.h>
#include <string.h>

// A queue serving scripted burst sizes: each poll returns the next entry,
// and a queue stays in its final state once the script runs out.
struct scripted_queue {
	const uint16_t *script;
	uint16_t length;
	uint16_t position;
};

static uint16_t
scripted_queue_burst(struct scripted_queue *queue, uint16_t read_size) {
	uint16_t burst_size = queue->script[queue->position];
	if (queue->position + 1 < queue->length) {
		++queue->position;
	}
	return burst_size > read_size ? read_size : burst_size;
}

// One round of an assignment's rx service over the production decision.
//
// Polls up to weight bursts, stopping at the first that does not fill
// the read window; returns the polls the round took.
static uint16_t
serve_round(struct scripted_queue *queue, uint16_t weight) {
	uint16_t polls = 0;
	for (uint16_t burst = 0; burst < weight; ++burst) {
		uint16_t read =
			scripted_queue_burst(queue, WORKER_RX_BURST_SIZE);
		++polls;
		if (!worker_rx_service_continues(read, WORKER_RX_BURST_SIZE)) {
			break;
		}
	}
	return polls;
}

// The configured service bound: the parser range and the staging
// allocation both size against it.
static void
test_weight_bound(void) {
	assert(WORKER_RX_WEIGHT_MAX == 16);
}

// A full queue costs the whole weight every round; a drained queue costs
// a single poll, whatever the weight.
static void
test_per_round_polls(void) {
	const uint16_t full[] = {32};
	struct scripted_queue queue = {
		.script = full,
		.length = 1,
	};
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 1) == 1);

	queue.position = 0;
	assert(serve_round(&queue, 16) == 16);
	assert(serve_round(&queue, 16) == 16);

	const uint16_t empty[] = {0};
	queue.script = empty;
	queue.length = 1;
	queue.position = 0;
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 16) == 1);
}

// A short burst inside the budget ends the assignment's service for the
// round; the next round starts fresh at one poll on the drained queue.
static void
test_early_stop(void) {
	const uint16_t drain_after_two[] = {32, 32, 5, 0};
	struct scripted_queue queue = {
		.script = drain_after_two,
		.length = 4,
	};
	assert(serve_round(&queue, 4) == 3);
	assert(serve_round(&queue, 4) == 1);
	assert(serve_round(&queue, 4) == 1);
}

// Weight 1 is the degenerate single-assignment service: one poll per
// round, and a full queue never earns a second one.
static void
test_weight_one_degenerate(void) {
	const uint16_t full_then_short[] = {32, 3};
	struct scripted_queue queue = {
		.script = full_then_short,
		.length = 2,
	};
	assert(serve_round(&queue, 1) == 1);
	assert(serve_round(&queue, 1) == 1);
}

// Assignments are served in configuration order, every round, each on
// its own budget.
static void
test_cyclic_order(void) {
	const uint16_t full[] = {32};
	struct scripted_queue queues[2] = {
		{.script = full, .length = 1},
		{.script = full, .length = 1},
	};
	const uint16_t weights[2] = {2, 1};

	for (uint32_t round = 0; round < 3; ++round) {
		uint16_t polls[2];
		for (uint32_t asgn_idx = 0; asgn_idx < 2; ++asgn_idx) {
			polls[asgn_idx] = serve_round(
				&queues[asgn_idx], weights[asgn_idx]
			);
		}
		assert(polls[0] == 2);
		assert(polls[1] == 1);
	}
}

// A two-segment mbuf chain over caller-owned frame bytes: the head holds
// head_len bytes, the tail the rest, and the head reports the chain
// totals the way a scattering NIC or a ring peer publishes them.
struct chained_fixture {
	struct rte_mbuf head;
	struct rte_mbuf tail;
};

static struct chained_fixture
chained_mbuf(
	const void *head_data,
	uint16_t head_len,
	const void *tail_data,
	uint16_t tail_len
) {
	struct chained_fixture fixture;
	memset(&fixture, 0, sizeof(fixture));
	fixture.head.buf_addr = (void *)head_data;
	fixture.head.data_len = head_len;
	fixture.head.pkt_len = (uint32_t)head_len + tail_len;
	fixture.head.nb_segs = 2;
	fixture.head.next = &fixture.tail;
	fixture.tail.buf_addr = (void *)tail_data;
	fixture.tail.data_len = tail_len;
	return fixture;
}

// An IPv6 frame whose Hop-by-Hop header straddles the segment boundary:
// the head ends four bytes into the extension header, so a parser
// bounding by the whole-chain length reads the rest of the header
// through a head-segment pointer. The head is the Ethernet header, the
// IPv6 header with Hop-by-Hop as its next header and a 32 byte payload,
// and the first four option bytes, and the tail carries the remaining
// options and the TCP header with ports 12345 to 80.
static void
test_rx_admission_refuses_ext_header_straddle(void) {
	const uint8_t head[] = {
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0xaa, 0xbb, 0xcc, 0xdd,
		0xee, 0xff, 0x86, 0xdd, 0x60, 0x00, 0x00, 0x00, 0x00, 0x20,
		0x00, 0x40, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x20, 0x01,
		0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x02, 0x06, 0x00, 0x01, 0x02,
	};
	const uint8_t tail[] = {
		0x03, 0x04, 0x05, 0x06, 0x30, 0x39, 0x00, 0x50, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x50, 0x02, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00,
	};
	struct chained_fixture fixture =
		chained_mbuf(head, sizeof(head), tail, sizeof(tail));
	assert(!worker_rx_admits(&fixture.head));
}

// A frame whose head segment ends inside the Ethernet header, ten bytes
// in. Parsing would bound the header read by the chain total and leave
// the network offset beyond the head's data length, the disagreement
// that underflows the encap length math of tunnel modules. The tail
// completes the Ethernet header and carries IPv4 and TCP headers with
// ports 12345 to 80.
static void
test_rx_admission_refuses_split_ethernet_header(void) {
	const uint8_t head[] = {
		0x11,
		0x22,
		0x33,
		0x44,
		0x55,
		0x66,
		0xaa,
		0xbb,
		0xcc,
		0xdd,
	};
	const uint8_t tail[] = {
		0xee, 0xff, 0x08, 0x00, 0x45, 0x00, 0x00, 0x2c, 0x00, 0x00,
		0x00, 0x00, 0x40, 0x06, 0x00, 0x00, 0x0a, 0x00, 0x00, 0x01,
		0x0a, 0x00, 0x00, 0x02, 0x30, 0x39, 0x00, 0x50, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x50, 0x02, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00,
	};
	struct chained_fixture fixture =
		chained_mbuf(head, sizeof(head), tail, sizeof(tail));
	assert(!worker_rx_admits(&fixture.head));
}

// The same single segment every ordinary rx burst delivers is admitted:
// Ethernet, IPv6 with TCP as its next header, ports 12345 to 80.
static void
test_rx_admission_accepts_contiguous(void) {
	const uint8_t frame[] = {
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0xaa, 0xbb, 0xcc, 0xdd,
		0xee, 0xff, 0x86, 0xdd, 0x60, 0x00, 0x00, 0x00, 0x00, 0x18,
		0x06, 0x40, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x20, 0x01,
		0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x02, 0x30, 0x39, 0x00, 0x50, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x50, 0x02, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00,
	};
	struct rte_mbuf mbuf;
	memset(&mbuf, 0, sizeof(mbuf));
	mbuf.buf_addr = (void *)frame;
	mbuf.data_len = sizeof(frame);
	mbuf.pkt_len = sizeof(frame);
	mbuf.nb_segs = 1;
	assert(worker_rx_admits(&mbuf));
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	test_weight_bound();
	test_per_round_polls();
	test_early_stop();
	test_weight_one_degenerate();
	test_cyclic_order();
	test_rx_admission_refuses_ext_header_straddle();
	test_rx_admission_refuses_split_ethernet_header();
	test_rx_admission_accepts_contiguous();

	return 0;
}
