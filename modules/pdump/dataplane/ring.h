#pragma once

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

#define __ALIGN4RING(val) (((val) + 3) & ~3)

// Header of the ring buffer, located at the beginning of the buffer.
// A pointer to this header points to the ring buffer itself.
struct ring_buffer {
	// write_idx indicates the next logical index for a worker to write to.
	_Atomic uint64_t write_idx;
	// readable_idx is the logical index of the next valid ring_msg_hdr.
	_Atomic uint64_t readable_idx;

	// Size represents the total size of the ring buffer;
	// The data portion's size is hdr->size minus sizeof(hdr).
	uint32_t size;
	uint32_t mask;
	// Offset within shared memory to the ring buffer data.
	uint8_t *data;
};

// This header precedes each message in the ring buffer and contains the total
// message length and packet metadata.
struct ring_msg_hdr {
	// Total size of the message, including the header and following
	// payload.
	uint32_t total_len;
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
	// While the occupied space exceeds the maximum occupied space,
	// indicating available free space for payload writing to the ring
	// buffer.
	while ((ring->write_idx - ring->readable_idx) >
	       (ring->size - payload_size)) {
		// STATEMENT: We can skip the boundary crossing check when
		// reading the slot size due to ring buffer alignment.
		uint8_t *pos = ring_data + (ring->readable_idx & ring->mask);
		uint32_t readable_slot_size = *(uint32_t *)pos;
		readable_slot_size = __ALIGN4RING(readable_slot_size);

		atomic_fetch_add_explicit(
			&ring->readable_idx,
			readable_slot_size,
			memory_order_relaxed
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
	assert(ring->size > offset + size);

	size_t n = 0;
	// This loop handles the case where the write extends beyond the ring
	// buffer's boundary. If the tail is less than the size of the data
	// being written, the next iteration will return a tail equal to the
	// entire ring buffer.
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
	// Alignment is critical for the pdump_ring_prepare function to work
	// properly.
	size = __ALIGN4RING(size);
	atomic_fetch_add_explicit(&ring->write_idx, size, memory_order_relaxed);
}
