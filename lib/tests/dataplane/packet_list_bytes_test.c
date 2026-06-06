#include <stdint.h>
#include <string.h>

#include <rte_mbuf.h>

#include "common/test_assert.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 0

// Brute-force sum: walk the chain and sum packet_data_len for each packet.
static uint64_t
brute_force_bytes(struct packet_list *list) {
	uint64_t sum = 0;
	for (struct packet *p = list->first; p != NULL; p = p->next) {
		sum += packet_data_len(p);
	}
	return sum;
}

// Build a single-packet mbuf with the given data_len. The mbuf is allocated
// with aligned_alloc (via alloc_mbuf) and must be freed with free_packet.
static int
make_packet(struct packet *p, uint16_t data_len) {
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, data_len, DEFAULT_TAILROOM);
	if (p->mbuf == NULL) {
		return -1;
	}
	p->next = NULL;
	return 0;
}

// Test: add several packets, verify bytes at each step, then pop to empty.
static int
test_add_and_pop(void) {
	static const uint16_t lens[] = {100, 200, 300, 50};
	size_t n = sizeof(lens) / sizeof(lens[0]);
	struct packet pkts[4];
	struct packet_list list;

	packet_list_init(&list);
	TEST_ASSERT_EQUAL(list.bytes, 0, "initial bytes");

	uint64_t expected = 0;
	for (size_t idx = 0; idx < n; idx++) {
		TEST_ASSERT_SUCCESS(
			make_packet(&pkts[idx], lens[idx]), "alloc"
		);
		packet_list_add(&list, &pkts[idx]);
		expected += lens[idx];
		TEST_ASSERT_EQUAL(
			list.bytes, expected, "bytes after add[%zu]", idx
		);
		TEST_ASSERT_EQUAL(
			list.bytes,
			brute_force_bytes(&list),
			"brute-force match after add[%zu]",
			idx
		);
	}

	// Pop all packets and verify bytes decrements correctly.
	while (packet_list_first(&list) != NULL) {
		struct packet *popped = packet_list_pop(&list);
		expected -= packet_data_len(popped);
		TEST_ASSERT_EQUAL(list.bytes, expected, "bytes after pop");
		TEST_ASSERT_EQUAL(
			list.bytes,
			brute_force_bytes(&list),
			"brute-force match after pop"
		);
		free_packet(popped);
	}

	TEST_ASSERT_EQUAL(list.bytes, 0, "bytes after pop to empty");
	TEST_ASSERT_EQUAL(list.count, 0, "count after pop to empty");

	return TEST_SUCCESS;
}

// Test: concat non-empty src into non-empty dst, and concat empty src.
static int
test_concat(void) {
	struct packet pa, pb, pc;
	struct packet_list dst, src;

	TEST_ASSERT_SUCCESS(make_packet(&pa, 111), "alloc pa");
	TEST_ASSERT_SUCCESS(make_packet(&pb, 222), "alloc pb");
	TEST_ASSERT_SUCCESS(make_packet(&pc, 333), "alloc pc");

	packet_list_init(&dst);
	packet_list_init(&src);

	packet_list_add(&dst, &pa);
	packet_list_add(&src, &pb);
	packet_list_add(&src, &pc);

	TEST_ASSERT_EQUAL(dst.bytes, 111, "dst before concat");
	TEST_ASSERT_EQUAL(src.bytes, 555, "src before concat");

	// Normal concat: both non-empty.
	packet_list_concat(&dst, &src);
	TEST_ASSERT_EQUAL(dst.bytes, 666, "dst after concat");
	TEST_ASSERT_EQUAL(
		dst.bytes, brute_force_bytes(&dst), "brute-force after concat"
	);

	// Concat empty src into dst: bytes must not change.
	struct packet_list empty;
	packet_list_init(&empty);
	packet_list_concat(&dst, &empty);
	TEST_ASSERT_EQUAL(dst.bytes, 666, "dst after concat-empty");

	// Concat non-empty src into empty dst: whole-struct copy path.
	struct packet pd;
	TEST_ASSERT_SUCCESS(make_packet(&pd, 77), "alloc pd");
	struct packet_list src2;
	packet_list_init(&src2);
	packet_list_add(&src2, &pd);
	struct packet_list dst2;
	packet_list_init(&dst2);
	packet_list_concat(&dst2, &src2);
	TEST_ASSERT_EQUAL(dst2.bytes, 77, "dst2 after empty-dst concat");
	TEST_ASSERT_EQUAL(
		dst2.bytes,
		brute_force_bytes(&dst2),
		"brute-force after empty-dst concat"
	);

	// Clean up by popping all from dst.
	struct packet *p;
	while ((p = packet_list_pop(&dst)) != NULL) {
		free_packet(p);
	}
	while ((p = packet_list_pop(&dst2)) != NULL) {
		free_packet(p);
	}

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"add_and_pop", test_add_and_pop},
		{"concat", test_concat},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; idx++) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	if (failed == 0) {
		LOG(INFO,
		    "=== All %zu packet_list_bytes tests passed ===",
		    total);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu packet_list_bytes tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
