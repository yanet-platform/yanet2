#pragma once

#include <stddef.h>
#include <stdint.h>

// Metadata in front of a captured packet's bytes inside a ring record.
//
// The ring's own 8-byte frame carries the record length, so this block
// starts at offset 8 and holds only the fields a reader needs to make
// sense of the packet bytes that follow it: the captured bytes start at
// offset 32 from this block's own start, offset 40 from the record's
// start. It is DPDK-free so a Go cgo preamble can decode it directly.
// Fields keep a fixed width and position; a reader copies the block out
// before reading it, because a ring record is only 4-byte aligned.
struct pdump_record_hdr {
	uint32_t magic;
	uint32_t packet_len;
	uint64_t timestamp;
	uint32_t worker_idx;
	uint32_t pipeline_idx;
	uint16_t rx_device_id;
	uint16_t tx_device_id;
	uint8_t queue; // enum pdump_mode bitmap
	uint8_t reserved[3];
};

_Static_assert(
	sizeof(struct pdump_record_hdr) == 32,
	"pdump record metadata must be the 32-byte wire size"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, magic) == 0,
	"a Go cgo reader decodes magic at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, packet_len) == 4,
	"a Go cgo reader decodes packet_len at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, timestamp) == 8,
	"a Go cgo reader decodes timestamp at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, worker_idx) == 16,
	"a Go cgo reader decodes worker_idx at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, pipeline_idx) == 20,
	"a Go cgo reader decodes pipeline_idx at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, rx_device_id) == 24,
	"a Go cgo reader decodes rx_device_id at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, tx_device_id) == 26,
	"a Go cgo reader decodes tx_device_id at a fixed offset"
);
_Static_assert(
	offsetof(struct pdump_record_hdr, queue) == 28,
	"a Go cgo reader decodes queue at a fixed offset"
);

// Magic value identifying a pdump record's metadata block.
#define PDUMP_RECORD_MAGIC 0xDEADBEEFu

// Smallest ring capacity a pdump config may capture into.
//
// A record is at most the 8-byte ring frame, this metadata block and 65535
// captured bytes, since a packet's length fits 16 bits. A ring takes
// records up to its capacity minus one eviction chunk of at most 4 KiB,
// so 128 KiB is the smallest power of two that holds every record.
#define PDUMP_MIN_RING_CAPACITY (128u * 1024u)
