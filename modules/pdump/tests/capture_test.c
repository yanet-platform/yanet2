/*
 * Pdump capture onto a ring object.
 *
 * Pins the producer contract: one publish per handler call, no seqno gap
 * across a filtered packet, a record payload laid out as the 32-byte
 * pdump metadata followed by the captured bytes, mode selecting which
 * queue's packets a config captures and tags, and a missing ring link
 * leaving capture disabled without touching any ring. An oversize record
 * is not covered here: it is unreachable in production (bind refuses
 * rings under PDUMP_MIN_RING_CAPACITY), and tests/common/ring_test.c
 * already pins that a refused record writes nothing and takes no seqno.
 */

#include <stdatomic.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <rte_eal.h>
#include <rte_mbuf.h>

#include "common/ring.h"
#include "common/test_assert.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include "modules/pdump/dataplane/config.h"
#include "modules/pdump/dataplane/dataplane.h"
#include "modules/pdump/dataplane/record.h"

#include "objects/ring/api/ring_object.h"

#include "fixture.h"

#define PDUMP_TEST_RING_CAPACITY 4096u
#define PDUMP_TEST_RX_DEVICE_ID 7
#define PDUMP_TEST_TX_DEVICE_ID 9
#define PDUMP_TEST_WORKER_COUNT 2
#define PDUMP_TEST_MAX_RECORDS 64

static void
run_handler(struct pdump_test_fixture *fx, struct packet_front *pf) {
	fx->module->handler(&fx->dp_worker, &fx->module_ectx, pf);
}

// One pdump record as a reader decodes it: the generic frame, the
// metadata block and the captured bytes.
//
// Copied out field by field, because a ring record is only 4-byte
// aligned.
struct pdump_test_record {
	uint32_t total_len;
	uint32_t seqno;
	struct pdump_record_hdr hdr;
	uint8_t payload[256];
};

// Copy one record at the given logical ring position into rec, the way
// a real reader must: every field is copied out before it is read,
// because a ring record is only 4-byte aligned.
static void
read_ring_record(
	struct ring_worker *ring,
	uint8_t *ring_data,
	uint64_t pos,
	struct pdump_test_record *rec
) {
	uint32_t mask = ring->local.mask;

	struct ring_record_frame frame;
	for (size_t i = 0; i < sizeof(frame); ++i) {
		((uint8_t *)&frame)[i] = ring_data[(pos + i) & mask];
	}
	rec->total_len = frame.total_len;
	rec->seqno = frame.seqno;

	uint64_t hdr_pos = pos + sizeof(frame);
	for (size_t i = 0; i < sizeof(rec->hdr); ++i) {
		((uint8_t *)&rec->hdr)[i] = ring_data[(hdr_pos + i) & mask];
	}

	uint32_t payload_len = frame.total_len - (uint32_t)sizeof(frame) -
			       (uint32_t)sizeof(rec->hdr);
	if (payload_len > sizeof(rec->payload)) {
		payload_len = sizeof(rec->payload);
	}
	uint64_t payload_pos = hdr_pos + sizeof(rec->hdr);
	for (uint32_t i = 0; i < payload_len; ++i) {
		rec->payload[i] = ring_data[(payload_pos + i) & mask];
	}
}

// Acquire the writer's published boundary before decoding ring records.
static uint64_t
load_published(const _Atomic uint64_t *pos) {
	return atomic_load_explicit(pos, memory_order_acquire);
}

// Decode every record published from the given position into out.
//
// The walk must land exactly on the published write position: a gap or an
// overrun would mean a corrupt or short record. out must hold at least max
// records; more records than that fail the test.
static int
read_published_records(
	struct pdump_test_fixture *fx,
	uint64_t from,
	struct pdump_test_record *out,
	int max,
	int *count
) {
	uint64_t write_idx = load_published(&fx->ring->published.write_idx);
	uint64_t pos = from;
	int seen = 0;
	while (pos < write_idx) {
		TEST_ASSERT(
			seen < max,
			"more published records than the case expects"
		);
		read_ring_record(fx->ring, fx->ring_data, pos, &out[seen]);
		pos += ring_align4(out[seen].total_len);
		++seen;
	}
	TEST_ASSERT_EQUAL(
		(long)pos,
		(long)write_idx,
		"records must exactly fill the published range"
	);
	*count = seen;
	return TEST_SUCCESS;
}

// One handler call publishes its records and filtered packets leave no seqno
// gap.
//
// The call commits fewer records than the ring's publish batch, yet they
// are readable once it returns, and a packet the filter rejects between
// accepted ones takes no sequence number.
static int
run_publish_per_call_filtered_no_gap_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	const bool accept[] = {true, false, true, false, true};
	const int total = (int)(sizeof(accept) / sizeof(accept[0]));
	int expected_records = 0;
	for (int i = 0; i < total; ++i) {
		if (accept[i]) {
			++expected_records;
		}
	}
	TEST_ASSERT(
		expected_records < (int)RING_PUBLISH_BATCH_DEFAULT,
		"the scenario needs fewer accepted packets than the publish "
		"batch, so the handler's own end-of-call publish is what "
		"makes them readable"
	);

	for (int i = 0; i < total; ++i) {
		struct packet *packet =
			pdump_test_build_packet(ut, accept[i], 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build a test packet");
		packet_front_input(pf, packet);
	}

	run_handler(fx, pf);

	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&pf->input),
		0L,
		"the handler must always pass every input packet"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&pf->output),
		(long)total,
		"every packet, filtered or not, must reach the output list"
	);

	struct pdump_test_record records[PDUMP_TEST_MAX_RECORDS];
	int count = 0;
	TEST_ASSERT_SUCCESS(
		read_published_records(
			fx, 0, records, PDUMP_TEST_MAX_RECORDS, &count
		),
		"failed to read the published records"
	);
	TEST_ASSERT_EQUAL(
		(long)count,
		(long)expected_records,
		"only the accepted packets must produce a record"
	);
	for (int i = 0; i < count; ++i) {
		TEST_ASSERT_EQUAL(
			(long)records[i].seqno,
			(long)i,
			"record %d must carry seqno %d, leaving no gap for the "
			"filtered packets interspersed between accepted ones",
			i,
			i
		);
	}

	return TEST_SUCCESS;
}

// A record's payload is the 32-byte pdump metadata, naming the capturing
// worker, the original packet and its devices, followed by
// min(snaplen, data_len) bytes of the packet itself.
static int
run_record_layout_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	const uint32_t snaplen = fx->config.snaplen;
	const uint16_t packet_len = 60;
	// The source bytes the captured record's payload must match,
	// snapshotted before capture runs.
	uint8_t expected[64];
	TEST_ASSERT(
		snaplen < packet_len && snaplen <= sizeof(expected),
		"the case needs a snaplen below the %u-byte packet",
		packet_len
	);

	struct packet *packet = pdump_test_build_packet(ut, true, packet_len);
	TEST_ASSERT_NOT_NULL(packet, "failed to build a test packet");
	packet->rx_device_id = PDUMP_TEST_RX_DEVICE_ID;
	packet->device_id = PDUMP_TEST_TX_DEVICE_ID;
	memcpy(expected,
	       rte_pktmbuf_mtod(packet_to_mbuf(packet), uint8_t *),
	       snaplen);
	packet_front_input(pf, packet);

	run_handler(fx, pf);

	struct pdump_test_record records[PDUMP_TEST_MAX_RECORDS];
	int count = 0;
	TEST_ASSERT_SUCCESS(
		read_published_records(
			fx, 0, records, PDUMP_TEST_MAX_RECORDS, &count
		),
		"failed to read the published records"
	);
	TEST_ASSERT_EQUAL(
		(long)count,
		1L,
		"the accepted packet must produce exactly one record"
	);

	struct pdump_test_record *rec = &records[0];
	uint32_t expected_total_len =
		(uint32_t)sizeof(struct ring_record_frame) +
		(uint32_t)sizeof(struct pdump_record_hdr) + snaplen;
	TEST_ASSERT_EQUAL(
		(long)rec->total_len,
		(long)expected_total_len,
		"total_len must be the frame, the metadata and the capped "
		"capture length"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.magic,
		(long)PDUMP_RECORD_MAGIC,
		"magic must identify the metadata block"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.worker_idx,
		(long)fx->dp_worker.idx,
		"worker_idx must name the capturing worker"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.packet_len,
		(long)packet_len,
		"packet_len must be the original packet length, not the "
		"capped capture length"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.timestamp,
		(long)PDUMP_TEST_TIMESTAMP,
		"timestamp must be the worker clock the handler read"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.rx_device_id,
		(long)PDUMP_TEST_RX_DEVICE_ID,
		"rx_device_id must name the packet's receiving device"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.device_id,
		(long)PDUMP_TEST_TX_DEVICE_ID,
		"device_id must name the device whose pipeline executed the "
		"packet"
	);
	TEST_ASSERT_EQUAL(
		(long)rec->hdr.queue,
		(long)PDUMP_INPUT,
		"queue must name the input list"
	);
	TEST_ASSERT_EQUAL(
		memcmp(rec->payload, expected, snaplen),
		0,
		"the captured bytes must be the packet's first min(snaplen, "
		"data_len) bytes"
	);

	return TEST_SUCCESS;
}

// One row of the mode table.
//
// The drop-list and input-list packets have distinguishable lengths, so a
// record's packet length proves which one produced it.
struct pdump_mode_row {
	const char *name;
	enum pdump_mode mode;
	uint16_t drop_len;
	uint16_t input_len;
	// DROPS mode never reads the input list, so it produces one record;
	// ALL mode reads both lists, drop first, for two.
	int expected_records;
};

// A config's mode selects the lists it captures and tags each record.
//
// DROPS captures only the drop list; ALL captures both, drop before input.
// Neither mode drains the drop list itself, which the pipeline still
// routes, and the input list reaches the output list in every mode.
static int
run_mode_selects_and_tags_queue_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	static const struct pdump_mode_row rows[] = {
		{"drops", PDUMP_DROPS, 60, 80, 1},
		{"all", PDUMP_ALL, 60, 80, 2},
	};

	uint64_t from = 0;
	for (size_t i = 0; i < sizeof(rows) / sizeof(rows[0]); ++i) {
		const struct pdump_mode_row *row = &rows[i];
		fx->config.mode = row->mode;

		struct packet *dropped =
			pdump_test_build_packet(ut, true, row->drop_len);
		TEST_ASSERT_NOT_NULL(dropped, "failed to build a test packet");
		packet_front_drop(pf, dropped);

		struct packet *input =
			pdump_test_build_packet(ut, true, row->input_len);
		TEST_ASSERT_NOT_NULL(input, "failed to build a test packet");
		packet_front_input(pf, input);

		run_handler(fx, pf);

		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&pf->drop),
			1L,
			"%s: the drop list must stay for the pipeline to "
			"route, "
			"not be drained",
			row->name
		);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&pf->output),
			1L,
			"%s: the input packet must still reach the output list",
			row->name
		);

		struct pdump_test_record records[PDUMP_TEST_MAX_RECORDS];
		int count = 0;
		TEST_ASSERT_SUCCESS(
			read_published_records(
				fx,
				from,
				records,
				PDUMP_TEST_MAX_RECORDS,
				&count
			),
			"%s: failed to read the published records",
			row->name
		);
		TEST_ASSERT_EQUAL(
			(long)count,
			(long)row->expected_records,
			"%s: unexpected record count",
			row->name
		);
		TEST_ASSERT_EQUAL(
			(long)records[0].hdr.queue,
			(long)PDUMP_DROPS,
			"%s: the first record must come from the drop queue",
			row->name
		);
		TEST_ASSERT_EQUAL(
			(long)records[0].hdr.packet_len,
			(long)row->drop_len,
			"%s: the first record must be the drop-list packet",
			row->name
		);
		if (count > 1) {
			TEST_ASSERT_EQUAL(
				(long)records[1].hdr.queue,
				(long)PDUMP_INPUT,
				"%s: the second record must come from the "
				"input "
				"queue",
				row->name
			);
			TEST_ASSERT_EQUAL(
				(long)records[1].hdr.packet_len,
				(long)row->input_len,
				"%s: the second record must be the input-list "
				"packet",
				row->name
			);
		}

		from = load_published(&fx->ring->published.write_idx);
		pdump_test_free_front(pf);
	}

	return TEST_SUCCESS;
}

// A config with no ring link leaves capture disabled: nothing is
// written to the ring it would otherwise use, and every packet still
// passes.
static int
run_no_ring_link_captures_nothing_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	fx->config.ring_link_idx = PDUMP_RING_LINK_NONE;
	pdump_test_fixture_commit(fx);

	struct packet *packet = pdump_test_build_packet(ut, true, 60);
	TEST_ASSERT_NOT_NULL(packet, "failed to build a test packet");
	packet_front_input(pf, packet);

	run_handler(fx, pf);

	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&pf->input),
		0L,
		"the handler must still pass the packet with no ring link"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&pf->output),
		1L,
		"the handler must still pass the packet with no ring link"
	);
	TEST_ASSERT_EQUAL(
		(long)load_published(&fx->ring->published.write_idx),
		0L,
		"a config with no ring link must never write to the ring it "
		"would otherwise use"
	);

	return TEST_SUCCESS;
}

// Check bounded links as well as identity, order and the append position.
static int
assert_packet_list(
	struct packet_list *list, struct packet *const *expected, size_t count
) {
	struct packet *packet = list->first;
	for (size_t idx = 0; idx < count; ++idx) {
		TEST_ASSERT_EQUAL(
			packet, expected[idx], "packet identity at %zu", idx
		);
		packet = packet->next;
	}
	TEST_ASSERT_NULL(packet, "list must end after the expected packets");
	TEST_ASSERT_EQUAL(
		list->last,
		count == 0 ? NULL : &expected[count - 1]->next,
		"append position must follow the final packet"
	);
	return TEST_SUCCESS;
}

// A captured prefix remains readable while all packets keep their queue path.
static int
run_rate_prefix_and_refill_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	struct packet *packets[43];
	struct packet *dropped[2];
	for (unsigned idx = 0; idx < 2; ++idx) {
		dropped[idx] = pdump_test_build_packet(ut, false, 60);
		TEST_ASSERT_NOT_NULL(
			dropped[idx], "failed to build dropped packet"
		);
		packet_front_drop(pf, dropped[idx]);
	}
	for (unsigned idx = 0; idx < 43; ++idx) {
		bool matches = (idx >= 3 && idx < 35) || idx == 38;
		struct packet *packet =
			pdump_test_build_packet(ut, matches, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packets[idx] = packet;
		packet_front_input(pf, packet);
	}
	run_handler(fx, pf);
	TEST_ASSERT_EQUAL(packet_front_output_count(pf), 43, "all inputs pass");
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->input, NULL, 0), "input drained"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->output, packets, 43),
		"prefix forwarding"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->drop, dropped, 2), "drop unchanged"
	);
	struct pdump_test_record records[PDUMP_TEST_MAX_RECORDS];
	int count = 0;
	TEST_ASSERT_SUCCESS(
		read_published_records(
			fx, 0, records, PDUMP_TEST_MAX_RECORDS, &count
		),
		"read prefix"
	);
	TEST_ASSERT_EQUAL(count, 32, "one worker spends the whole burst");
	for (int idx = 0; idx < count; ++idx) {
		TEST_ASSERT_EQUAL(
			records[idx].seqno,
			(uint32_t)idx,
			"prefix has no sequence gap"
		);
	}
	uint64_t *skipped = counter_get_address(
		fx->config.rate_skipped_counter_id,
		fx->module_ectx.abs_counter_storage
	);
	TEST_ASSERT_EQUAL(
		*skipped,
		5,
		"only refused match and unexamined tail count as skips"
	);
	pdump_test_free_front(pf);
	packet_front_init(pf);

	uint64_t from = load_published(&fx->ring->published.write_idx);
	fx->dp_worker.current_time += 1000000;
	struct packet *packet;
	for (unsigned idx = 0; idx < 2; ++idx) {
		dropped[idx] = pdump_test_build_packet(ut, false, 60);
		TEST_ASSERT_NOT_NULL(
			dropped[idx], "failed to build dropped packet"
		);
		packet_front_drop(pf, dropped[idx]);
		packet = pdump_test_build_packet(ut, true, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packets[idx] = packet;
		packet_front_input(pf, packet);
	}
	run_handler(fx, pf);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->input, NULL, 0),
		"input drained after refill"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->output, packets, 2), "refill forwarding"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->drop, dropped, 2),
		"drop unchanged after refill"
	);
	TEST_ASSERT_SUCCESS(
		read_published_records(
			fx, from, records, PDUMP_TEST_MAX_RECORDS, &count
		),
		"read refill"
	);
	TEST_ASSERT_EQUAL(
		count, 1, "one millisecond refills exactly one packet"
	);
	TEST_ASSERT_EQUAL(*skipped, 6, "rejection adds no debt before refill");
	return TEST_SUCCESS;
}

// Exhausted queues count every unexamined packet and preserve both lists.
static int
run_rate_exhausted_both_queues_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	for (unsigned idx = 0; idx < 32; ++idx) {
		TEST_ASSERT(
			token_bucket_take(
				&fx->rate_bucket, fx->dp_worker.current_time
			),
			"fill bucket"
		);
	}
	fx->config.mode = PDUMP_ALL;
	pdump_test_fixture_commit(fx);
	struct packet *packets[7];
	for (unsigned idx = 0; idx < 7; ++idx) {
		struct packet *packet = pdump_test_build_packet(ut, false, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packets[idx] = packet;
		if (idx < 3) {
			packet_front_drop(pf, packet);
		} else {
			packet_front_input(pf, packet);
		}
	}
	run_handler(fx, pf);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->input, NULL, 0),
		"exhausted input drained"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->output, packets + 3, 4),
		"exhausted input forwarding"
	);
	TEST_ASSERT_SUCCESS(
		assert_packet_list(&pf->drop, packets, 3),
		"exhausted drop unchanged"
	);
	TEST_ASSERT_EQUAL(
		packet_front_output_count(pf), 4, "all input packets pass"
	);
	TEST_ASSERT_EQUAL(
		packet_front_drop_count(pf), 3, "drop queue stays intact"
	);
	TEST_ASSERT_EQUAL(
		load_published(&fx->ring->published.write_idx),
		0,
		"exhaustion writes nothing"
	);
	uint64_t *skipped = counter_get_address(
		fx->config.rate_skipped_counter_id,
		fx->module_ectx.abs_counter_storage
	);
	TEST_ASSERT_EQUAL(
		*skipped, 7, "both unexamined queues count every packet"
	);
	return TEST_SUCCESS;
}

// Worker-local counters accompany one shared config budget, not worker shares.
static int
run_rate_shared_workers_and_independent_configs_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	for (unsigned idx = 0; idx < 32; ++idx) {
		struct packet *packet = pdump_test_build_packet(ut, true, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packet_front_input(pf, packet);
	}
	run_handler(fx, pf);
	TEST_ASSERT_EQUAL(
		fx->ring->local.next_seqno,
		32,
		"skewed worker spends full burst"
	);
	pdump_test_free_front(pf);
	packet_front_init(pf);

	struct module_ectx other = fx->module_ectx;
	other.abs_module_prepared = calloc(1, fx->module->prepared_size);
	TEST_ASSERT_NOT_NULL(
		other.abs_module_prepared, "allocate second context"
	);
	other.abs_config_gen_ectx = pdump_test_gen_worker_ectx(fx->gen, 1);
	other.abs_counter_storage = counter_storage_spawn(
		ADDR_OF(&fx->config.cp_module.counter_registry.memory_context),
		NULL,
		&fx->config.cp_module.counter_registry
	);
	TEST_ASSERT_NOT_NULL(
		other.abs_counter_storage, "allocate worker-local counters"
	);
	struct dp_worker worker = fx->dp_worker;
	worker.idx = 1;
	fx->module->commit_ectx_handler(&other, &fx->config.cp_module);
	struct packet *packet = pdump_test_build_packet(ut, true, 60);
	TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
	packet_front_input(pf, packet);
	fx->module->handler(&worker, &other, pf);
	struct ring_worker *other_ring = ring_object_worker(fx->ring_object, 1);
	TEST_ASSERT_EQUAL(
		other_ring->local.next_seqno,
		0,
		"second worker has no separate credit"
	);
	uint64_t *skipped = counter_get_address(
		fx->config.rate_skipped_counter_id, other.abs_counter_storage
	);
	TEST_ASSERT_EQUAL(
		*skipped, 1, "second worker owns its skipped counter"
	);
	uint64_t *first_skipped = counter_get_address(
		fx->config.rate_skipped_counter_id,
		fx->module_ectx.abs_counter_storage
	);
	TEST_ASSERT_EQUAL(
		*first_skipped, 0, "first worker's counter is unchanged"
	);
	pdump_test_free_front(pf);
	packet_front_init(pf);

	struct pdump_module_config independent = {0};
	struct token_bucket bucket;
	pdump_rate_init(&bucket, 1000, fx->dp_worker.current_time);
	independent.mode = PDUMP_INPUT;
	independent.snaplen = fx->config.snaplen;
	independent.rate_pps = 1000;
	independent.rate_skipped_counter_id =
		fx->config.rate_skipped_counter_id;
	SET_OFFSET_OF(
		&independent.ebpf_program, ADDR_OF(&fx->config.ebpf_program)
	);
	SET_OFFSET_OF(&independent.rate_bucket, &bucket);
	other.abs_cp_module = &independent.cp_module;
	fx->module->commit_ectx_handler(&other, &independent.cp_module);
	for (unsigned idx = 0; idx < 32; ++idx) {
		packet = pdump_test_build_packet(ut, true, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packet_front_input(pf, packet);
	}
	fx->module->handler(&worker, &other, pf);
	TEST_ASSERT_EQUAL(
		other_ring->local.next_seqno,
		32,
		"new config on same ring has independent credit"
	);
	TEST_ASSERT_EQUAL(*skipped, 1, "no skips for independent config burst");
	counter_storage_free(other.abs_counter_storage);
	free(other.abs_module_prepared);
	return TEST_SUCCESS;
}

// Unlimited capture leaves the budget and skip counter out of the hot path.
static int
run_rate_unlimited_body(
	struct dataplane_ut *ut,
	struct pdump_test_fixture *fx,
	struct packet_front *pf
) {
	for (unsigned idx = 0; idx < 40; ++idx) {
		struct packet *packet = pdump_test_build_packet(ut, true, 60);
		TEST_ASSERT_NOT_NULL(packet, "failed to build packet");
		packet_front_input(pf, packet);
	}
	run_handler(fx, pf);
	TEST_ASSERT_EQUAL(
		fx->ring->local.next_seqno,
		40,
		"unlimited captures beyond burst"
	);
	uint64_t *skipped = counter_get_address(
		fx->config.rate_skipped_counter_id,
		fx->module_ectx.abs_counter_storage
	);
	TEST_ASSERT_EQUAL(*skipped, 0, "unlimited capture never rate-skips");
	TEST_ASSERT_EQUAL(packet_front_output_count(pf), 40, "all inputs pass");
	return TEST_SUCCESS;
}

// The pdump minimum ring capacity holds the largest record pdump writes.
//
// The largest record is the ring frame, the metadata and 65535 captured
// bytes. A change to the ring's eviction chunk that shrinks what a ring of
// that capacity takes breaks this test instead of losing packets.
static int
run_pdump_min_ring_capacity_holds_largest_record_test(struct dataplane_ut *ut) {
	(void)ut;

	uint32_t capacity = PDUMP_MIN_RING_CAPACITY;
	TEST_ASSERT_EQUAL(
		(long)(capacity & (capacity - 1)),
		0L,
		"the minimum capacity must be a power of two a ring accepts"
	);

	struct ring_worker ring;
	ring_worker_init(&ring, capacity, RING_PUBLISH_BATCH_DEFAULT);
	uint64_t largest = RING_RECORD_FRAME_SIZE +
			   sizeof(struct pdump_record_hdr) + UINT16_MAX;
	TEST_ASSERT(
		ring_worker_batch_max(&ring) >= largest,
		"a ring of the minimum capacity takes records up to %u bytes, "
		"the largest pdump record is %lu",
		ring_worker_batch_max(&ring),
		(unsigned long)largest
	);

	return TEST_SUCCESS;
}

// One capture scenario: the fixture it needs and the body that drives a
// handler call through it.
struct pdump_capture_case {
	const char *name;
	uint32_t capacity;
	uint32_t snaplen;
	uint64_t worker_idx;
	uint64_t rate_pps;
	int (*body)(
		struct dataplane_ut *ut,
		struct pdump_test_fixture *fx,
		struct packet_front *pf
	);
};

// Build the case's fixture, run its body and always tear the fixture down.
//
// A failing case never leaks its agent or ring object into the cases that
// run after it.
static int
run_pdump_capture_case(
	struct dataplane_ut *ut,
	struct module *module,
	struct pdump_test_gen *gen,
	const struct pdump_capture_case *tc
) {
	char agent_name[64];
	char ring_name[64];
	snprintf(agent_name, sizeof(agent_name), "pdump-%s", tc->name);
	snprintf(ring_name, sizeof(ring_name), "ring-%s", tc->name);

	yanet_error *err = NULL;
	struct rte_bpf *bpf =
		pdump_test_compile_accept_ip_filter(tc->snaplen, &err);
	TEST_ASSERT_NOT_NULL(
		bpf,
		"failed to compile the ip filter: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct pdump_test_fixture_params params = {
		.agent_name = agent_name,
		.ring_name = ring_name,
		.capacity = tc->capacity,
		.publish_batch = RING_PUBLISH_BATCH_DEFAULT,
		.snaplen = tc->snaplen,
		.worker_idx = tc->worker_idx,
		.rate_pps = tc->rate_pps,
		.module = module,
		.bpf = bpf,
		.gen = gen,
	};

	struct pdump_test_fixture fx;
	struct packet_front pf;
	packet_front_init(&pf);

	int res = pdump_test_fixture_build(ut, &params, &fx);
	if (res == TEST_SUCCESS) {
		res = tc->body(ut, &fx, &pf);
	}

	pdump_test_free_front(&pf);
	pdump_test_fixture_destroy(&fx);
	free(bpf);

	return res;
}

int
main(void) {
	log_enable_name("debug");

	// A minimal, hugepage-free EAL setup: just enough for rte_bpf_load's
	// mmap-based allocation and rte_malloc's heap, since this harness
	// never touches a NIC or a hugepage-backed mempool. It shares no EAL
	// config file, so test binaries running in parallel never contend
	// for the EAL lock.
	char prog_name[] = "pdump_capture_test";
	char *eal_argv[] = {
		prog_name,
		"--no-huge",
		"--no-pci",
		"-m",
		"128",
		"--iova-mode=va",
		"--no-shconf"
	};
	if (rte_eal_init(sizeof(eal_argv) / sizeof(eal_argv[0]), eal_argv) <
	    0) {
		fprintf(stderr, "rte_eal_init failed\n");
		return 1;
	}

	const char *objs_to_load[] = {RING_OBJECT_TYPE};
	const char *modules[] = {"pdump"};
	struct dataplane_ut_config cfg = {
		// Detached agents retain arenas; allow for sanitizer red zones.
		.cp_memory = 1u << 28,
		.dp_memory = 1u << 20,
		.worker_count = 2,
		.modules = modules,
		.module_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct module *module = new_module_pdump();
	if (module == NULL) {
		fprintf(stderr, "new_module_pdump failed\n");
		dataplane_ut_free(ut);
		return 1;
	}

	struct pdump_test_gen gen;
	if (pdump_test_gen_init(&gen, PDUMP_TEST_WORKER_COUNT) !=
	    TEST_SUCCESS) {
		fprintf(stderr, "pdump_test_gen_init failed\n");
		free(module);
		dataplane_ut_free(ut);
		return 1;
	}

	struct pdump_capture_case cases[] = {
		{"publish-per-call",
		 PDUMP_TEST_RING_CAPACITY,
		 128,
		 0,
		 0,
		 run_publish_per_call_filtered_no_gap_body},
		{"record-layout",
		 PDUMP_TEST_RING_CAPACITY,
		 40,
		 1,
		 0,
		 run_record_layout_body},
		{"mode-select",
		 PDUMP_TEST_RING_CAPACITY,
		 128,
		 0,
		 0,
		 run_mode_selects_and_tags_queue_body},
		{"no-ring-link",
		 PDUMP_TEST_RING_CAPACITY,
		 128,
		 0,
		 0,
		 run_no_ring_link_captures_nothing_body},
		{"rate-prefix-refill",
		 PDUMP_TEST_RING_CAPACITY,
		 40,
		 0,
		 1000,
		 run_rate_prefix_and_refill_body},
		{"rate-full-queues",
		 PDUMP_TEST_RING_CAPACITY,
		 40,
		 0,
		 1000,
		 run_rate_exhausted_both_queues_body},
		{"rate-shared-workers",
		 PDUMP_TEST_RING_CAPACITY,
		 40,
		 0,
		 1000,
		 run_rate_shared_workers_and_independent_configs_body},

		{"rate-unlimited",
		 PDUMP_TEST_RING_CAPACITY,
		 40,
		 0,
		 0,
		 run_rate_unlimited_body},
	};

	int res = TEST_SUCCESS;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		LOG(INFO, "%s running", cases[i].name);
		res = run_pdump_capture_case(ut, module, &gen, &cases[i]);
		if (res != TEST_SUCCESS) {
			LOG(ERROR, "%s failed", cases[i].name);
			break;
		}
		LOG(INFO, "%s passed", cases[i].name);
	}

	if (res == TEST_SUCCESS) {
		LOG(INFO, "min_ring_capacity_holds_largest_record running");
		res = run_pdump_min_ring_capacity_holds_largest_record_test(ut);
		LOG(res == TEST_SUCCESS ? INFO : ERROR,
		    "min_ring_capacity_holds_largest_record %s",
		    res == TEST_SUCCESS ? "passed" : "failed");
	}

	pdump_test_gen_destroy(&gen);
	free(module);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
