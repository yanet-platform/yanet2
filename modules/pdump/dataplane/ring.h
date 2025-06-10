#pragma once

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

#ifndef unlikely
#define unlikely(x) __builtin_expect(!!(x), 0)
#endif

// Align value to 4-byte boundary for consistent ring buffer alignment
#define __ALIGN4RING(val) (((val) + 3) & ~3)

// Header of the ring buffer, located at the beginning of the buffer.
// A pointer to this header points to the ring buffer itself.
struct ring_buffer {
	// write_idx indicates the next logical index for a worker to write to.
	_Atomic uint64_t write_idx;
	// readable_idx is the logical index of the next valid ring_msg_hdr.
	// This is advanced by the writer when space is needed (overwriting old
	// data).
	_Atomic uint64_t readable_idx;

	// Size represents the total size of the ring buffer;
	// The data portion's size is size minus sizeof(struct ring_buffer).
	uint32_t size;
	// Mask for efficient modulo operation (size must be power of 2)
	uint32_t mask;
	// Pointer to the ring buffer data area
	uint8_t *data;
};

// Magic number to validate ring message headers
#define RING_MSG_MAGIC 0xDEADBEEF

// This header precedes each message in the ring buffer and contains the total
// message length and packet metadata.
struct ring_msg_hdr {
	// Total size of the message, including the header and following
	// payload. NOTE: total_len must be the first member.
	uint32_t total_len;
	// Magic number for header validation
	uint32_t magic;
	// packet_len indicates the length of the original packet.
	uint32_t packet_len;
	// Timestamp indicating when the packet was captured.
	uint64_t timestamp;
	// Worker that processes this message; this index is used to select the
	// appropriate ring buffer.
	uint32_t worker_idx;
	// Index of the pipeline where the pdump module is located.
	uint32_t pipeline_idx;
	// ID of the device from which the packet was received.
	uint16_t rx_device_id;
	// ID of the device to which the packet may be sent.
	uint16_t tx_device_id;
	// Indicates whether the packet was processed from the drop queue or
	// from the input queue.
	// uint8_t for now, potentially refactorable as a bitfield.
	uint8_t is_drops;
	uint8_t reserved[3];
};

static inline void
pdump_ring_prepare(
	struct ring_buffer *ring, uint8_t *ring_data, uint32_t payload_size
) {
	uint32_t aligned_payload_size = __ALIGN4RING(payload_size);
	assert(ring->size >= aligned_payload_size);
	assert(ring->write_idx >= ring->readable_idx);

	// While the occupied space (write_idx - readable_idx) exceeds the
	// available space needed for the new payload, advance readable_idx
	// to free up space by discarding old messages.
	while ((ring->write_idx - ring->readable_idx) >
	       (ring->size - aligned_payload_size)) {
		// Read the size of the message at readable_idx to know how much
		// space to free. We can safely read uint32_t directly because
		// all writes are aligned to 4-byte boundaries by
		// pdump_ring_checkpoint, ensuring the total_len field is always
		// properly aligned.
		uint8_t *pos = ring_data + (ring->readable_idx & ring->mask);
		uint32_t readable_slot_size = *(uint32_t *)pos;
		readable_slot_size = __ALIGN4RING(readable_slot_size);

		if (unlikely(
			    !readable_slot_size ||
			    ring->readable_idx + readable_slot_size >
				    ring->write_idx
		    )) {
			// When invalid data is detected at the current position
			// and advancing readable_idx would either exceed
			// write_idx or cause an infinite loop, reset
			// readable_idx to write_idx to indicate that no
			// readable data remains in the buffer.
			atomic_store_explicit(
				&ring->readable_idx,
				ring->write_idx,
				memory_order_release
			);
			return;
		}

		// Advance readable_idx past this message to free its space
		atomic_fetch_add_explicit(
			&ring->readable_idx,
			readable_slot_size,
			memory_order_release
		);
	}
}

static inline void
pdump_ring_write(
	struct ring_buffer *ring,
	uint8_t *ring_data,
	uint64_t offset,
	uint8_t *payload,
	uint64_t size
) {
	assert(ring->size >= offset + size);

	size_t n = 0;
	// Handle writes that may wrap around the ring buffer boundary.
	// Split the write into chunks that don't cross the boundary.
	while (n < size) {
		uint64_t write_idx =
			(ring->write_idx + offset + n) & ring->mask;
		uint64_t tail = ring->size - write_idx;
		uint64_t remains = size - n;
		uint64_t write_size = remains > tail ? tail : remains;

		assert(write_size > 0);
		memcpy(ring_data + write_idx, payload + n, write_size);
		n += write_size;
	}
}

static inline void
pdump_ring_checkpoint(struct ring_buffer *ring, uint32_t size) {
	// Align size to 4-byte boundary - this alignment is critical for
	// pdump_ring_prepare to safely read message sizes without boundary
	// checks.
	size = __ALIGN4RING(size);
	// Use release ordering to ensure all data writes are visible to readers
	// before the write_idx update makes the data available for consumption.
	atomic_fetch_add_explicit(&ring->write_idx, size, memory_order_release);
}

static inline void
pdump_ring_write_msg(
	struct ring_buffer *ring,
	uint8_t *ring_data,
	struct ring_msg_hdr *hdr,
	uint8_t *payload
) {
	// Step 1. Move readable_idx to make space for new data
	pdump_ring_prepare(ring, ring_data, hdr->total_len);

	// Step 2. Write ring_msg_hdr struct
	uint64_t hdr_size = sizeof(*hdr);
	pdump_ring_write(ring, ring_data, 0, (uint8_t *)hdr, hdr_size);

	// Step 3. Write mbuf data to an offset equal to hdr_size.
	uint64_t payload_size = hdr->total_len - hdr_size;
	pdump_ring_write(ring, ring_data, hdr_size, payload, payload_size);

	// Step 4. Store write_idx atomically, considering alignment.
	pdump_ring_checkpoint(ring, hdr->total_len);
}
