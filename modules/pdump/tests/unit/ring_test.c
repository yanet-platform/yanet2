/* System headers */
#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

/* Project headers */
#include "dataplane/mode.h"
#include "modules/pdump/dataplane/ring.h"
#include <lib/logging/log.h>

#include "helpers.h"

/* Test constants */
#define RING_SIZE_SMALL 64
#define RING_SIZE_MEDIUM 256
#define RING_SIZE_LARGE 1024
#define RING_SIZE_HUGE 4096

#define TEST_PAYLOAD_SIZE_SMALL 8
#define TEST_PAYLOAD_SIZE_MEDIUM 64
#define TEST_PAYLOAD_SIZE_LARGE 256

/* Test data patterns */
static const char test_pattern_a[] = "AAAAAAAA";
static const char test_pattern_b[] = "BBBBBBBB";
static const char test_pattern_c[] = "CCCCCCCC";

/**
 * @brief Initialize a new ring buffer with given size
 *
 * @param ring_size Size of the ring buffer
 * @return struct ring_buffer Initialized ring buffer structure
 */
static struct ring_buffer
init_ring_buffer(uint32_t ring_size) {
	struct ring_buffer ring = {0};
	uint8_t *ring_data = malloc(ring_size);
	if (ring_data != NULL) {
		memset(ring_data, 0, ring_size);
	}
	ring.size = ring_size;
	ring.mask = ring_size - 1;
	ring.data = ring_data;
	return ring;
}

/**
 * @brief Test ring buffer initialization
 *
 * Tests:
 * - Ring buffer structure initialization
 * - Atomic indices initialization to zero
 * - Size and mask calculation
 * - Data pointer assignment
 */
static int
test_ring_init() {
	uint32_t ring_size = RING_SIZE_MEDIUM;

	/* Initialize ring structure */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Verify initialization */
	TEST_ASSERT_EQUAL(ring.size, ring_size, "Ring size mismatch");
	TEST_ASSERT_EQUAL(ring.mask, ring_size - 1, "Ring mask mismatch");
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx), 0, "Write index should be 0"
	);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.readable_idx), 0, "Readable index should be 0"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test __ALIGN4RING macro
 *
 * Tests:
 * - Alignment to 4-byte boundaries
 * - Edge cases (0, 1, 3, 4, 5, etc.)
 * - Large values
 */
static int
test_align4ring_macro() {
	/* Test basic alignment cases */
	TEST_ASSERT_EQUAL(__ALIGN4RING(0), 0, "Alignment of 0 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(1), 4, "Alignment of 1 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(2), 4, "Alignment of 2 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(3), 4, "Alignment of 3 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(4), 4, "Alignment of 4 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(5), 8, "Alignment of 5 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(8), 8, "Alignment of 8 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(9), 12, "Alignment of 9 failed");

	/* Test larger values */
	TEST_ASSERT_EQUAL(__ALIGN4RING(15), 16, "Alignment of 15 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(16), 16, "Alignment of 16 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(17), 20, "Alignment of 17 failed");

	/* Test very large values */
	TEST_ASSERT_EQUAL(__ALIGN4RING(1023), 1024, "Alignment of 1023 failed");
	TEST_ASSERT_EQUAL(__ALIGN4RING(1024), 1024, "Alignment of 1024 failed");

	return TEST_SUCCESS;
}

/**
 * @brief Test ring message header structure
 *
 * Tests:
 * - Header size and alignment
 * - Maximum queue id (enum pdump_mode)
 */
static int
test_ring_msg_hdr() {
	/* Test header size - should be properly aligned */
	TEST_ASSERT_EQUAL(
		sizeof(struct ring_msg_hdr) % 4,
		0,
		"Ring message header size not 4-byte aligned"
	);

	TEST_ASSERT(
		PDUMP_ALL <= UINT8_MAX,
		"enum pdump_mode is out of range (max u8)"
	);

	return TEST_SUCCESS;
}

/**
 * @brief Test basic ring write operation
 *
 * Tests:
 * - Single write operation
 * - Data integrity
 * - Index advancement
 * - Boundary checks
 */
static int
test_ring_write_basic() {
	uint32_t ring_size = RING_SIZE_MEDIUM;
	const char *test_data = test_pattern_a;
	uint32_t data_len = strlen(test_data);

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Write data */
	pdump_ring_write(&ring, ring.data, 0, (uint8_t *)test_data, data_len);

	/* Verify data was written correctly */
	TEST_ASSERT_EQUAL(
		memcmp(ring.data, test_data, data_len),
		0,
		"Written data does not match expected"
	);

	/* Verify indices haven't changed (write doesn't advance indices) */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		0,
		"Write index changed unexpectedly"
	);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.readable_idx),
		0,
		"Readable index changed unexpectedly"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring write with wraparound
 *
 * Tests:
 * - Write operation that crosses ring boundary
 * - Data integrity across wraparound
 * - Proper handling of ring mask
 */
static int
test_ring_write_wraparound() {
	uint32_t ring_size = 64; /* Small ring for easy wraparound */
	const char *test_data = "ABCDEFGHIJ"; /* Shorter data to fit */
	uint32_t data_len = strlen(test_data);
	uint32_t write_offset = ring_size - 5; /* Start near end of ring */

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	ring.write_idx = write_offset;
	ring.readable_idx = 0;

	/* Write data that will wrap around - offset is relative to write_idx */
	pdump_ring_write(&ring, ring.data, 0, (uint8_t *)test_data, data_len);

	/* Verify data was written correctly with wraparound */
	uint32_t first_chunk = ring_size - write_offset;
	uint32_t second_chunk = data_len - first_chunk;

	/* Check first chunk at the end of ring */
	TEST_ASSERT_EQUAL(
		memcmp(ring.data + write_offset, test_data, first_chunk),
		0,
		"First chunk of wrapped data incorrect"
	);

	/* Check second chunk at the beginning of ring */
	TEST_ASSERT_EQUAL(
		memcmp(ring.data, test_data + first_chunk, second_chunk),
		0,
		"Second chunk of wrapped data incorrect"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring checkpoint operation
 *
 * Tests:
 * - Index advancement
 * - Alignment handling
 * - Atomic operations
 */
static int
test_ring_checkpoint() {
	uint32_t ring_size = RING_SIZE_MEDIUM;

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Test checkpoint with aligned size */
	pdump_ring_checkpoint(&ring, 16);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		16,
		"Write index not advanced correctly"
	);

	/* Test checkpoint with unaligned size (should be aligned up) */
	pdump_ring_checkpoint(&ring, 15);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		32,
		"Write index not aligned correctly"
	);

	/* Test checkpoint with size 1 (should align to 4) */
	pdump_ring_checkpoint(&ring, 1);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx), 36, "Write index alignment failed"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring prepare operation
 *
 * Tests:
 * - Space availability checking
 * - Old message cleanup
 * - Index advancement during cleanup
 */
static int
test_ring_prepare_basic() {
	uint32_t ring_size = RING_SIZE_SMALL;
	uint32_t payload_size = 16;

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Test prepare with empty ring */
	pdump_ring_prepare(&ring, ring.data, payload_size);

	/* Indices should remain unchanged for empty ring */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		0,
		"Write index changed unexpectedly"
	);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.readable_idx),
		0,
		"Readable index changed unexpectedly"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring prepare with space reclamation
 *
 * Tests:
 * - Automatic space reclamation when ring is full
 * - Proper advancement of readable_idx
 * - Message size reading from ring
 */
static int
test_ring_prepare_reclaim() {
	uint32_t ring_size = 64; /* Small ring for easy testing */
#define MSG_SIZE 20
	uint32_t msg_size = MSG_SIZE;
	// Reserve space for the emulated header (total_len field)
	uint8_t payload[MSG_SIZE - sizeof(msg_size)] =
		"Data message"; /* Initialize with zeros */

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Fill ring with messages to force reclamation */
	/* Write several messages */
	for (int i = 0; i < 3; i++) {
		/* Prepare space for message */
		pdump_ring_prepare(&ring, ring.data, msg_size);

		/* Write message size using pdump_ring_write (emulate msg_hdr
		 * write) */
		pdump_ring_write(
			&ring,
			ring.data,
			0,
			(uint8_t *)&msg_size,
			sizeof(msg_size)
		);
		/* Write message data using pdump_ring_write */
		pdump_ring_write(
			&ring,
			ring.data,
			sizeof(msg_size),
			payload,
			sizeof(payload)
		);

		/* Advance write index to commit the message */
		pdump_ring_checkpoint(&ring, msg_size);
	}

	uint64_t initial_readable_idx = atomic_load(&ring.readable_idx);

	/* Now prepare for a large message that requires space reclamation */
	uint32_t large_payload =
		ring_size - 10; /* Larger than available space */
	pdump_ring_prepare(&ring, ring.data, large_payload);

	/* readable_idx should have advanced to free space */
	TEST_ASSERT(
		atomic_load(&ring.readable_idx) > initial_readable_idx,
		"Readable index should have advanced during space reclamation"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test complete message write cycle
 *
 * Tests:
 * - Complete workflow: prepare -> write header -> write payload -> checkpoint
 * - Message integrity
 * - Proper index management
 */
static int
test_complete_message_cycle() {
	uint32_t ring_size = RING_SIZE_MEDIUM;
	const char *payload = test_pattern_a;
	uint32_t payload_len = strlen(payload);
	struct ring_msg_hdr hdr;

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Prepare message header */
	uint32_t total_len = sizeof(struct ring_msg_hdr) + payload_len;
	memset(&hdr, 0, sizeof(hdr));
	hdr.total_len = total_len;
	hdr.magic = RING_MSG_MAGIC;
	hdr.packet_len = payload_len;
	hdr.timestamp = 1234567890ULL;

	uint64_t write_idx_before = atomic_load(&ring.write_idx);
	/* Write message using pdump_ring_write_msg */
	pdump_ring_write_msg(&ring, ring.data, &hdr, (uint8_t *)payload);

	/* Verify write advanced write index */
	uint64_t write_idx_after = atomic_load(&ring.write_idx);
	uint32_t aligned_total_len = __ALIGN4RING(total_len);
	TEST_ASSERT_EQUAL(
		write_idx_after,
		write_idx_before + aligned_total_len,
		"Write index not advanced correctly after checkpoint"
	);

	/* Verify message integrity */
	struct ring_msg_hdr *written_hdr = (struct ring_msg_hdr *)ring.data;
	TEST_ASSERT_EQUAL(
		written_hdr->total_len, total_len, "Header total_len mismatch"
	);
	TEST_ASSERT_EQUAL(
		written_hdr->magic, RING_MSG_MAGIC, "Header magic mismatch"
	);
	TEST_ASSERT_EQUAL(
		written_hdr->packet_len,
		payload_len,
		"Header packet_len mismatch"
	);
	TEST_ASSERT_EQUAL(
		written_hdr->timestamp,
		1234567890ULL,
		"Header timestamp mismatch"
	);

	/* Verify payload integrity */
	char *written_payload =
		(char *)(ring.data + sizeof(struct ring_msg_hdr));
	TEST_ASSERT_EQUAL(
		memcmp(written_payload, payload, payload_len),
		0,
		"Payload data mismatch"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test multiple messages in ring
 *
 * Tests:
 * - Multiple message storage
 * - Message boundaries
 * - Index tracking
 * - Message retrieval
 */
static int
test_multiple_messages() {
	uint32_t ring_size = RING_SIZE_LARGE;
	const char *payloads[] = {
		test_pattern_a, test_pattern_b, test_pattern_c
	};
	uint32_t num_messages = sizeof(payloads) / sizeof(payloads[0]);

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	uint64_t message_offsets[num_messages];

	/* Write multiple messages */
	for (uint32_t i = 0; i < num_messages; i++) {
		uint32_t payload_len = strlen(payloads[i]);
		uint32_t total_len = sizeof(struct ring_msg_hdr) + payload_len;
		struct ring_msg_hdr hdr;

		message_offsets[i] = atomic_load(&ring.write_idx);

		/* Prepare header */
		memset(&hdr, 0, sizeof(hdr));
		hdr.total_len = total_len;
		hdr.magic = RING_MSG_MAGIC;
		hdr.packet_len = payload_len;
		hdr.timestamp = 1000000ULL + i;

		/* Write message using pdump_ring_write_msg */
		pdump_ring_write_msg(
			&ring, ring.data, &hdr, (uint8_t *)payloads[i]
		);
	}

	/* Verify all messages */
	for (uint32_t i = 0; i < num_messages; i++) {
		uint64_t offset = message_offsets[i] & ring.mask;
		struct ring_msg_hdr *hdr =
			(struct ring_msg_hdr *)(ring.data + offset);
		char *payload = (char *)(ring.data + offset +
					 sizeof(struct ring_msg_hdr));

		TEST_ASSERT_EQUAL(
			hdr->magic,
			RING_MSG_MAGIC,
			"Message %u magic mismatch",
			i
		);
		TEST_ASSERT_EQUAL(
			hdr->timestamp,
			1000000ULL + i,
			"Message %u timestamp mismatch",
			i
		);
		TEST_ASSERT_EQUAL(
			memcmp(payload, payloads[i], strlen(payloads[i])),
			0,
			"Message %u payload mismatch",
			i
		);
	}

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring overflow and wraparound
 *
 * Tests:
 * - Ring buffer overflow handling
 * - Wraparound behavior
 * - Old message overwriting
 */
static int
test_ring_overflow() {
	uint32_t ring_size = 128;   /* Small ring to force overflow */
	uint32_t num_messages = 10; /* More than ring can hold */

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	uint64_t initial_readable_idx = atomic_load(&ring.readable_idx);

	/* Write many messages to force overflow */
	for (uint32_t i = 0; i < num_messages; i++) {
		struct ring_msg_hdr hdr;
		char payload[16];

		snprintf(payload, sizeof(payload), "MSG_%u", i);

		memset(&hdr, 0, sizeof(hdr));
		hdr.total_len = sizeof(hdr) + strlen(payload);
		hdr.magic = RING_MSG_MAGIC;
		hdr.packet_len = strlen(payload);

		/* Write message using pdump_ring_write_msg */
		pdump_ring_write_msg(
			&ring, ring.data, &hdr, (uint8_t *)payload
		);
	}

	/* readable_idx should have advanced due to space reclamation */
	TEST_ASSERT(
		atomic_load(&ring.readable_idx) > initial_readable_idx,
		"Readable index should advance during overflow"
	);

	/* write_idx should have wrapped around */
	TEST_ASSERT(
		atomic_load(&ring.write_idx) > ring_size,
		"Write index should exceed ring size after multiple writes"
	);

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test edge cases and error conditions
 *
 * Tests:
 * - Zero-size payloads
 * - Maximum size payloads
 * - Boundary conditions
 * - Invalid parameters
 */
static int
test_edge_cases() {
	uint32_t ring_size = RING_SIZE_MEDIUM;

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Test zero-size write */
	pdump_ring_write(&ring, ring.data, 0, NULL, 0);
	/* Should not crash */

	/* Test zero-size checkpoint */
	uint64_t write_idx_before = atomic_load(&ring.write_idx);
	pdump_ring_checkpoint(&ring, 0);
	/* Should align to 4 bytes minimum */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		write_idx_before,
		"Zero-size checkpoint should not advance index"
	);

	/* Test size 1 checkpoint (should align to 4) */
	pdump_ring_checkpoint(&ring, 1);
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx),
		write_idx_before + 4,
		"Size 1 checkpoint should align to 4 bytes"
	);

	/* Test large payload that fits exactly (-4 from previous checkpoint) */
	uint32_t max_payload = ring_size - sizeof(struct ring_msg_hdr) - 4;
	pdump_ring_prepare(&ring, ring.data, max_payload);
	/* Should not crash */

	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test stress scenarios with large data
 *
 * Tests:
 * - Large message handling
 * - Performance under stress
 * - Memory boundary conditions
 */
static int
test_stress_large_data() {
	uint32_t ring_size = RING_SIZE_HUGE;
	uint32_t large_payload_size = TEST_PAYLOAD_SIZE_LARGE;

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Create large payload */
	uint8_t *large_payload = malloc(large_payload_size);
	TEST_ASSERT_NOT_NULL(large_payload, "Failed to allocate large payload");

	/* Fill with pattern */
	for (uint32_t i = 0; i < large_payload_size; i++) {
		large_payload[i] = (uint8_t)(i % 256);
	}

	/* Write large message */
	struct ring_msg_hdr hdr;
	uint32_t total_len = sizeof(hdr) + large_payload_size;

	memset(&hdr, 0, sizeof(hdr));
	hdr.total_len = total_len;
	hdr.magic = RING_MSG_MAGIC;
	hdr.packet_len = large_payload_size;

	/* Write message using pdump_ring_write_msg */
	pdump_ring_write_msg(&ring, ring.data, &hdr, large_payload);

	/* Verify large message */
	struct ring_msg_hdr *written_hdr = (struct ring_msg_hdr *)ring.data;
	TEST_ASSERT_EQUAL(
		written_hdr->magic,
		RING_MSG_MAGIC,
		"Large message header magic mismatch"
	);
	TEST_ASSERT_EQUAL(
		written_hdr->packet_len,
		large_payload_size,
		"Large message packet_len mismatch"
	);

	/* Verify large payload */
	uint8_t *written_payload = ring.data + sizeof(struct ring_msg_hdr);
	TEST_ASSERT_EQUAL(
		memcmp(written_payload, large_payload, large_payload_size),
		0,
		"Large payload data mismatch"
	);

	free(large_payload);
	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test boundary conditions
 *
 * Tests:
 * - Ring size boundaries
 * - Index wraparound at maximum values
 * - Edge case alignments
 */
static int
test_boundary_conditions() {
	uint32_t ring_size = RING_SIZE_SMALL; /* 64 bytes */

	/* Setup ring */
	struct ring_buffer ring = init_ring_buffer(ring_size);
	TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

	/* Test 1: Maximum message size that fits exactly in ring */
	uint32_t max_msg_size = ring_size - sizeof(struct ring_msg_hdr);
	uint32_t max_total_len = sizeof(struct ring_msg_hdr) + max_msg_size;

	/* Ensure this is exactly at the boundary */
	TEST_ASSERT(
		max_total_len <= ring_size, "Max message should fit in ring"
	);

	struct ring_msg_hdr hdr;
	memset(&hdr, 0, sizeof(hdr));
	hdr.total_len = max_total_len;
	hdr.magic = RING_MSG_MAGIC;
	hdr.packet_len = max_msg_size;

	uint8_t *max_payload = malloc(max_msg_size);
	TEST_ASSERT_NOT_NULL(max_payload, "Failed to allocate max payload");
	memset(max_payload, 0xCC, max_msg_size);

	/* Write message using pdump_ring_write_msg */
	pdump_ring_write_msg(&ring, ring.data, &hdr, max_payload);

	/* Verify max size message */
	struct ring_msg_hdr *written_hdr = (struct ring_msg_hdr *)ring.data;
	TEST_ASSERT_EQUAL(
		written_hdr->magic, RING_MSG_MAGIC, "Max message magic mismatch"
	);
	TEST_ASSERT_EQUAL(
		written_hdr->packet_len,
		max_msg_size,
		"Max message size mismatch"
	);

	/* Test 2: Write index at exact ring boundary */
	ring.write_idx = ring_size - 1;
	ring.readable_idx = 0;

	/* Write small message that will wrap around */
	uint32_t wrap_msg_size = 8;
	uint32_t wrap_total_len = sizeof(struct ring_msg_hdr) + wrap_msg_size;

	pdump_ring_prepare(&ring, ring.data, wrap_total_len);
	/* Should handle wraparound correctly */

	/* Test 3: Indices at maximum values before wraparound */
	uint64_t max_idx = UINT64_MAX - ring_size;
	ring.write_idx = max_idx;
	ring.readable_idx = max_idx - ring_size / 2;

	/* Write message near index overflow boundary */
	pdump_ring_prepare(&ring, ring.data, 16);
	pdump_ring_checkpoint(&ring, 16);

	/* Verify indices wrapped correctly */
	uint64_t new_write_idx = atomic_load(&ring.write_idx);
	TEST_ASSERT(
		new_write_idx > max_idx, "Write index should have advanced"
	);

	/* Test 4: Alignment boundary conditions */
	ring.write_idx = 0;
	ring.readable_idx = 0;

	/* Test unaligned sizes that should be aligned up */
	pdump_ring_checkpoint(&ring, 1); /* Should align to 4 */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx), 4, "Size 1 should align to 4"
	);

	pdump_ring_checkpoint(&ring, 3); /* Should align to 8 */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx), 8, "Size 3 should align to 8"
	);

	pdump_ring_checkpoint(&ring, 5); /* Should align to 16 */
	TEST_ASSERT_EQUAL(
		atomic_load(&ring.write_idx), 16, "Size 5 should align to 16"
	);

	/* Test 5: Ring exactly full condition */
	ring.write_idx = ring_size;
	ring.readable_idx = 0;

	/* Available space should be 0 */
	uint64_t available = ring_size - (atomic_load(&ring.write_idx) -
					  atomic_load(&ring.readable_idx));
	TEST_ASSERT_EQUAL(available, 0, "Ring should be exactly full");

	/* Prepare should trigger space reclamation */
	uint64_t old_readable_idx = atomic_load(&ring.readable_idx);
	pdump_ring_prepare(&ring, ring.data, 16);
	TEST_ASSERT(
		atomic_load(&ring.readable_idx) > old_readable_idx,
		"Space reclamation should advance readable_idx"
	);

	free(max_payload);
	free(ring.data);
	return TEST_SUCCESS;
}

/**
 * @brief Test ring with power-of-2 sizes
 *
 * Tests:
 * - Various power-of-2 ring sizes
 * - Mask calculation correctness
 * - Wraparound behavior with different sizes
 */
static int
test_power_of_2_sizes() {
	uint32_t sizes[] = {64, 128, 256, 512, 1024, 2048};
	uint32_t num_sizes = sizeof(sizes) / sizeof(sizes[0]);

	for (uint32_t i = 0; i < num_sizes; i++) {
		uint32_t ring_size = sizes[i];

		/* Setup ring */
		struct ring_buffer ring = init_ring_buffer(ring_size);
		TEST_ASSERT_NOT_NULL(ring.data, "Failed to allocate ring data");

		/* Verify mask is correct */
		TEST_ASSERT_EQUAL(
			ring.mask,
			ring_size - 1,
			"Incorrect mask for ring size %u",
			ring_size
		);

		/* Test wraparound behavior */
		ring.write_idx = ring_size + 10;
		uint64_t wrapped_idx = atomic_load(&ring.write_idx) & ring.mask;
		TEST_ASSERT_EQUAL(
			wrapped_idx,
			10,
			"Wraparound failed for ring size %u",
			ring_size
		);

		free(ring.data);
	}

	return TEST_SUCCESS;
}

/* Test suite structure */
struct test_case {
	const char *name;
	int (*test_func)();
};

static struct test_case test_cases[] = {
	{"Ring Initialization", test_ring_init},
	{"ALIGN4RING Macro", test_align4ring_macro},
	{"Ring Message Header constraints", test_ring_msg_hdr},
	{"Basic Ring Write", test_ring_write_basic},
	{"Ring Write Wraparound", test_ring_write_wraparound},
	{"Ring Checkpoint", test_ring_checkpoint},
	{"Ring Prepare Basic", test_ring_prepare_basic},
	{"Ring Prepare Reclaim", test_ring_prepare_reclaim},
	{"Complete Message Cycle", test_complete_message_cycle},
	{"Multiple Messages", test_multiple_messages},
	{"Ring Overflow", test_ring_overflow},
	{"Edge Cases", test_edge_cases},
	{"Stress Large Data", test_stress_large_data},
	{"Boundary Conditions", test_boundary_conditions},
	{"Power of 2 Sizes", test_power_of_2_sizes},
};

/**
 * @brief Run all ring buffer tests
 *
 * @return Number of failed tests
 */
int
main() {
	log_enable_name("debug");

	int failed_tests = 0;
	int total_tests = sizeof(test_cases) / sizeof(test_cases[0]);

	LOG(INFO, "Starting ring buffer unit tests...");
	LOG(INFO, "Running %d test cases", total_tests);

	for (int i = 0; i < total_tests; i++) {
		LOG(INFO,
		    "Running test %d/%d: %s",
		    i + 1,
		    total_tests,
		    test_cases[i].name);

		int result = test_cases[i].test_func();
		if (result == TEST_SUCCESS) {
			LOG(INFO, "✓ PASSED: %s", test_cases[i].name);
		} else {
			LOG(ERROR, "✗ FAILED: %s", test_cases[i].name);
			failed_tests++;
		}
	}

	LOG(INFO,
	    "Test summary: %d/%d tests passed, %d failed",
	    total_tests - failed_tests,
	    total_tests,
	    failed_tests);

	if (failed_tests == 0) {
		LOG(INFO,
		    "All tests passed! Ring buffer implementation is "
		    "working correctly.");
		return 0;
	} else {
		LOG(ERROR,
		    "Some tests failed. Please review the implementation.");
		return 1;
	}
}
