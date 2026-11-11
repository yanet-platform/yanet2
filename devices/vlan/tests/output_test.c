/*
 * Egress length validation of the vlan device.
 *
 * The 802.1Q tag sits at offset 14 and spans 4 bytes, so a tagged frame
 * needs 18 bytes in the head segment before its tag may be read or
 * rewritten. Pins both tag paths of the output handler on that boundary:
 * frames below it are dropped as malformed, frames from it up are
 * stripped or retagged, an untagged frame is forwarded unchanged or
 * grown by an injected tag, and a tag crossing an mbuf segment boundary
 * is dropped rather than read past the head segment.
 */

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_mbuf.h>

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "devices/vlan/api/controlplane.h"
#include "devices/vlan/dataplane/config.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

// The output tag the retag and inject paths write; distinct from the TCI
// the test frames arrive with, so a rewrite is observable.
#define VLAN_TEST_VLAN 100
#define VLAN_TEST_FRAME_TCI 0x0ABC
// Smallest frame carrying a full tag: Ethernet header plus 802.1Q header.
#define VLAN_TEST_TAGGED_MIN_LEN                                               \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr))
// Loop bound covering both sides of the old 28-byte boundary.
#define VLAN_TEST_TAGGED_MAX_LEN 28
// Untagged probe frame long enough to carry payload after the header.
#define VLAN_TEST_UNTAGGED_LEN 20
// The smallest headroom the inject path accepts: its prepend guard
// reserves room for the injected tag and for the packet descriptor
// sharing the buffer below the data.
#define VLAN_TEST_INJECT_HEADROOM                                              \
	(sizeof(struct packet) + sizeof(struct rte_vlan_hdr))

// The handler from the loaded vlan device type, as a worker resolves it.
static device_handler test_output_handler;
// One device context per output tag, wired the way a published
// generation presents them.
static struct device_ectx untagged_ectx;
static struct device_ectx tagged_ectx;

// Fills an Ethernet header with position-distinct addresses, so a header
// moved by the wrong distance cannot reproduce the pattern.
static void
fill_ether_hdr(struct rte_ether_hdr *ether_hdr, uint16_t ether_type) {
	for (size_t idx = 0; idx < RTE_ETHER_ADDR_LEN; idx++) {
		ether_hdr->dst_addr.addr_bytes[idx] = (uint8_t)(0xA0 + idx);
		ether_hdr->src_addr.addr_bytes[idx] = (uint8_t)(0xB0 + idx);
	}
	ether_hdr->ether_type = rte_cpu_to_be_16(ether_type);
}

// Fills a frame of the requested length whose ethertype announces an
// 802.1Q tag; lengths below the 18-byte minimum leave the tag truncated,
// exactly as a malformed frame arrives.
static void
fill_tagged_frame(uint8_t *frame, size_t total_len) {
	fill_ether_hdr((struct rte_ether_hdr *)frame, RTE_ETHER_TYPE_VLAN);
	if (total_len >= VLAN_TEST_TAGGED_MIN_LEN) {
		struct rte_vlan_hdr *vlan_hdr =
			(struct rte_vlan_hdr *)(frame +
						sizeof(struct rte_ether_hdr));
		vlan_hdr->vlan_tci = rte_cpu_to_be_16(VLAN_TEST_FRAME_TCI);
		vlan_hdr->eth_proto = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	}
	for (size_t idx = VLAN_TEST_TAGGED_MIN_LEN; idx < total_len; idx++) {
		frame[idx] = (uint8_t)idx;
	}
}

// Fills an untagged IPv4 frame of the requested length with a position
// pattern as payload.
static void
fill_untagged_frame(uint8_t *frame, size_t total_len) {
	fill_ether_hdr((struct rte_ether_hdr *)frame, RTE_ETHER_TYPE_IPV4);
	for (size_t idx = sizeof(struct rte_ether_hdr); idx < total_len;
	     idx++) {
		frame[idx] = (uint8_t)idx;
	}
}

// Builds one single-segment packet carrying a copy of the frame behind
// the given headroom. Returns 0, or -1 when the mbuf allocation fails.
static int
frame_packet(
	struct packet *packet,
	const uint8_t *frame,
	size_t frame_len,
	uint16_t headroom
) {
	struct rte_mbuf *mbuf = alloc_mbuf(headroom, (uint16_t)frame_len, 0);
	if (mbuf == NULL) {
		return -1;
	}
	memcpy(rte_pktmbuf_mtod(mbuf, uint8_t *), frame, frame_len);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	packet->data_len = packet_data_len(packet);
	return 0;
}

// Builds a two-segment packet splitting the frame after the head length:
// the head mbuf ends there and the rest continues in a tight tail
// segment, so an access crossing the split reads past a segment. The
// parser verdict on the split frame is ignored: a header continuing in
// the tail is exactly the shape under test. Returns 0, or -1 when an
// mbuf allocation fails.
static int
split_frame_packet(
	struct packet *packet,
	const uint8_t *frame,
	size_t frame_len,
	size_t head_len
) {
	memset(packet, 0, sizeof(*packet));
	struct packet_info info = {
		.data = frame,
		.size = (uint16_t)frame_len,
	};
	uint16_t seg_sizes[2] = {
		(uint16_t)head_len,
		(uint16_t)(frame_len - head_len),
	};
	fill_packet_from_segments(packet, &info, seg_sizes, 2);
	return packet->mbuf != NULL ? 0 : -1;
}

// Where one egressed frame landed; the packet stays owned by the result
// for the caller to check and free.
struct vlan_egress_result {
	struct packet *packet;
	bool dropped;
};

// Egresses one frame through the vlan output handler of the given device
// context, reporting whether the handler emitted or dropped it.
static struct vlan_egress_result
vlan_egress(struct device_ectx *device_ectx, struct packet *packet) {
	struct packet_front front;
	packet_front_init(&front);
	packet_front_input(&front, packet);

	test_output_handler(NULL, device_ectx, &front);

	struct vlan_egress_result result;
	result.packet = packet_list_pop(&front.output);
	result.dropped = false;
	if (result.packet == NULL) {
		result.packet = packet_list_pop(&front.drop);
		result.dropped = result.packet != NULL;
	}
	return result;
}

// Checks the frame header still matches the fill pattern and carries the
// given ethertype.
static bool
frame_header_matches(struct rte_mbuf *mbuf, uint16_t ether_type) {
	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	for (size_t idx = 0; idx < RTE_ETHER_ADDR_LEN; idx++) {
		if (ether_hdr->dst_addr.addr_bytes[idx] !=
			    (uint8_t)(0xA0 + idx) ||
		    ether_hdr->src_addr.addr_bytes[idx] !=
			    (uint8_t)(0xB0 + idx)) {
			return false;
		}
	}
	return ether_hdr->ether_type == rte_cpu_to_be_16(ether_type);
}

// Reads the TCI of a tagged frame; the frame must be at least the 18-byte
// minimum for the read to stay in bounds.
static uint16_t
frame_vlan_tci(struct rte_mbuf *mbuf) {
	struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_vlan_hdr *, sizeof(struct rte_ether_hdr)
	);
	return rte_be_to_cpu_16(vlan_hdr->vlan_tci);
}

static int
run_strip_truncated_tag_test(void) {
	// A frame announcing a tag it does not fully carry is malformed:
	// the strip path drops it instead of reading past the frame end.
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	for (size_t total_len = sizeof(struct rte_ether_hdr);
	     total_len < VLAN_TEST_TAGGED_MIN_LEN;
	     total_len++) {
		fill_tagged_frame(frame, total_len);
		struct packet packet;
		TEST_ASSERT_SUCCESS(
			frame_packet(&packet, frame, total_len, 0),
			"tagged frame build failed"
		);

		struct vlan_egress_result result =
			vlan_egress(&untagged_ectx, &packet);
		TEST_ASSERT(
			result.dropped,
			"strip path must drop a %zu-byte tagged frame",
			total_len
		);
		free_packet(result.packet);
	}
	return TEST_SUCCESS;
}

static int
run_strip_tagged_boundary_test(void) {
	// From the 18-byte minimum up, the strip path removes the tag: the
	// frame shrinks by the tag size and carries the ethertype the tag
	// held.
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	for (size_t total_len = VLAN_TEST_TAGGED_MIN_LEN;
	     total_len <= VLAN_TEST_TAGGED_MAX_LEN;
	     total_len++) {
		fill_tagged_frame(frame, total_len);
		struct packet packet;
		TEST_ASSERT_SUCCESS(
			frame_packet(&packet, frame, total_len, 0),
			"tagged frame build failed"
		);

		struct vlan_egress_result result =
			vlan_egress(&untagged_ectx, &packet);
		TEST_ASSERT(
			!result.dropped && result.packet != NULL,
			"strip path must emit a %zu-byte tagged frame",
			total_len
		);

		struct rte_mbuf *mbuf = packet_to_mbuf(result.packet);
		TEST_ASSERT_EQUAL(
			rte_pktmbuf_pkt_len(mbuf),
			(uint32_t)(total_len - sizeof(struct rte_vlan_hdr)),
			"strip path must shrink a %zu-byte tagged frame by the "
			"tag",
			total_len
		);
		TEST_ASSERT(
			frame_header_matches(mbuf, RTE_ETHER_TYPE_IPV4),
			"strip path must expose the ethertype the tag held on "
			"a %zu-byte frame",
			total_len
		);
		free_packet(result.packet);
	}
	return TEST_SUCCESS;
}

static int
run_retag_truncated_tag_test(void) {
	// A frame announcing a tag it does not fully carry is malformed:
	// the retag path drops it instead of touching an absent tag.
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	for (size_t total_len = sizeof(struct rte_ether_hdr);
	     total_len < VLAN_TEST_TAGGED_MIN_LEN;
	     total_len++) {
		fill_tagged_frame(frame, total_len);
		struct packet packet;
		TEST_ASSERT_SUCCESS(
			frame_packet(&packet, frame, total_len, 0),
			"tagged frame build failed"
		);

		struct vlan_egress_result result =
			vlan_egress(&tagged_ectx, &packet);
		TEST_ASSERT(
			result.dropped,
			"retag path must drop a %zu-byte tagged frame",
			total_len
		);
		free_packet(result.packet);
	}
	return TEST_SUCCESS;
}

static int
run_retag_tagged_boundary_test(void) {
	// From the 18-byte minimum up, the retag path rewrites the TCI and
	// leaves the frame length and ethertype untouched.
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	for (size_t total_len = VLAN_TEST_TAGGED_MIN_LEN;
	     total_len <= VLAN_TEST_TAGGED_MAX_LEN;
	     total_len++) {
		fill_tagged_frame(frame, total_len);
		struct packet packet;
		TEST_ASSERT_SUCCESS(
			frame_packet(&packet, frame, total_len, 0),
			"tagged frame build failed"
		);

		struct vlan_egress_result result =
			vlan_egress(&tagged_ectx, &packet);
		TEST_ASSERT(
			!result.dropped && result.packet != NULL,
			"retag path must emit a %zu-byte tagged frame",
			total_len
		);

		struct rte_mbuf *mbuf = packet_to_mbuf(result.packet);
		TEST_ASSERT_EQUAL(
			rte_pktmbuf_pkt_len(mbuf),
			(uint32_t)total_len,
			"retag path must keep the %zu-byte frame length",
			total_len
		);
		TEST_ASSERT(
			frame_header_matches(mbuf, RTE_ETHER_TYPE_VLAN),
			"retag path must keep the tagged ethertype on a "
			"%zu-byte frame",
			total_len
		);
		TEST_ASSERT_EQUAL(
			frame_vlan_tci(mbuf),
			(uint16_t)VLAN_TEST_VLAN,
			"retag path must set the configured TCI on a %zu-byte "
			"frame",
			total_len
		);
		free_packet(result.packet);
	}
	return TEST_SUCCESS;
}

static int
run_untagged_passthrough_test(void) {
	// An untagged frame egressing an untagged device is forwarded
	// byte-for-byte unchanged.
	uint8_t frame[VLAN_TEST_UNTAGGED_LEN];
	fill_untagged_frame(frame, VLAN_TEST_UNTAGGED_LEN);
	struct packet packet;
	TEST_ASSERT_SUCCESS(
		frame_packet(&packet, frame, VLAN_TEST_UNTAGGED_LEN, 0),
		"untagged frame build failed"
	);

	struct vlan_egress_result result = vlan_egress(&untagged_ectx, &packet);
	TEST_ASSERT(
		!result.dropped && result.packet != NULL,
		"untagged device must emit an untagged frame"
	);

	struct rte_mbuf *mbuf = packet_to_mbuf(result.packet);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(mbuf),
		(uint32_t)VLAN_TEST_UNTAGGED_LEN,
		"untagged device must not change the frame length"
	);
	TEST_ASSERT(
		memcmp(rte_pktmbuf_mtod(mbuf, uint8_t *), frame, sizeof(frame)
		) == 0,
		"untagged device must not change any frame byte"
	);
	free_packet(result.packet);
	return TEST_SUCCESS;
}

static int
run_inject_tag_test(void) {
	// An untagged frame egressing a tagged device gains a tag: the frame
	// grows by the tag size, the tag carries the configured TCI and the
	// ethertype the header held, and the payload shifts with it.
	uint8_t frame[VLAN_TEST_UNTAGGED_LEN];
	fill_untagged_frame(frame, VLAN_TEST_UNTAGGED_LEN);
	struct packet packet;
	TEST_ASSERT_SUCCESS(
		frame_packet(
			&packet,
			frame,
			VLAN_TEST_UNTAGGED_LEN,
			VLAN_TEST_INJECT_HEADROOM
		),
		"untagged frame build failed"
	);

	struct vlan_egress_result result = vlan_egress(&tagged_ectx, &packet);
	TEST_ASSERT(
		!result.dropped && result.packet != NULL,
		"tagged device must emit an injected untagged frame"
	);

	struct rte_mbuf *mbuf = packet_to_mbuf(result.packet);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(mbuf),
		(uint32_t)(VLAN_TEST_UNTAGGED_LEN + sizeof(struct rte_vlan_hdr)
		),
		"tag injection must grow the frame by the tag size"
	);
	TEST_ASSERT(
		frame_header_matches(mbuf, RTE_ETHER_TYPE_VLAN),
		"tag injection must set the tagged ethertype"
	);
	TEST_ASSERT_EQUAL(
		frame_vlan_tci(mbuf),
		(uint16_t)VLAN_TEST_VLAN,
		"tag injection must set the configured TCI"
	);
	struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_vlan_hdr *, sizeof(struct rte_ether_hdr)
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(vlan_hdr->eth_proto),
		(uint16_t)RTE_ETHER_TYPE_IPV4,
		"injected tag must carry the ethertype the header held"
	);
	TEST_ASSERT(
		memcmp(rte_pktmbuf_mtod(mbuf, uint8_t *) +
			       VLAN_TEST_TAGGED_MIN_LEN,
		       frame + sizeof(struct rte_ether_hdr),
		       VLAN_TEST_UNTAGGED_LEN - sizeof(struct rte_ether_hdr)) ==
			0,
		"tag injection must keep the payload after the tag"
	);
	free_packet(result.packet);
	return TEST_SUCCESS;
}

// Egresses tagged frames split so the tag straddles the segment boundary
// at both ends of the accepted length range, asserting every one is
// dropped for the given path.
static int
run_split_drop_cases(struct device_ectx *device_ectx, const char *path) {
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	const size_t head_lens[] = {
		sizeof(struct rte_ether_hdr),
		VLAN_TEST_TAGGED_MIN_LEN - 1,
	};
	const size_t total_lens[] = {
		VLAN_TEST_TAGGED_MIN_LEN,
		VLAN_TEST_TAGGED_MAX_LEN,
	};
	for (size_t head_idx = 0;
	     head_idx < sizeof(head_lens) / sizeof(head_lens[0]);
	     head_idx++) {
		for (size_t total_idx = 0;
		     total_idx < sizeof(total_lens) / sizeof(total_lens[0]);
		     total_idx++) {
			size_t total_len = total_lens[total_idx];
			fill_tagged_frame(frame, total_len);
			struct packet packet;
			TEST_ASSERT_SUCCESS(
				split_frame_packet(
					&packet,
					frame,
					total_len,
					head_lens[head_idx]
				),
				"split tagged frame build failed"
			);

			struct vlan_egress_result result =
				vlan_egress(device_ectx, &packet);
			TEST_ASSERT(
				result.dropped,
				"%s must drop a %zu-byte frame split after %zu "
				"bytes",
				path,
				total_len,
				head_lens[head_idx]
			);
			free_packet(result.packet);
		}
	}
	return TEST_SUCCESS;
}

static int
run_strip_split_tag_test(void) {
	// A tag crossing the segment boundary is unreachable: the strip
	// path drops the frame instead of reading past the head segment.
	return run_split_drop_cases(&untagged_ectx, "strip path");
}

static int
run_retag_split_tag_test(void) {
	// A tag crossing the segment boundary is unreachable: the retag
	// path drops the frame instead of writing past the head segment.
	return run_split_drop_cases(&tagged_ectx, "retag path");
}

static int
run_strip_chained_tag_in_head_test(void) {
	// A tag fully inside the head segment is stripped even when the
	// frame continues in another segment.
	uint8_t frame[VLAN_TEST_TAGGED_MAX_LEN];
	size_t total_len = VLAN_TEST_TAGGED_MIN_LEN + 4;
	fill_tagged_frame(frame, total_len);
	struct packet packet;
	TEST_ASSERT_SUCCESS(
		split_frame_packet(
			&packet, frame, total_len, VLAN_TEST_TAGGED_MIN_LEN
		),
		"split tagged frame build failed"
	);

	struct vlan_egress_result result = vlan_egress(&untagged_ectx, &packet);
	TEST_ASSERT(
		!result.dropped && result.packet != NULL,
		"strip path must emit a split frame whose tag sits in the head"
	);

	struct rte_mbuf *mbuf = packet_to_mbuf(result.packet);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(mbuf),
		(uint32_t)(total_len - sizeof(struct rte_vlan_hdr)),
		"strip path must shrink a split frame by the tag"
	);
	TEST_ASSERT_EQUAL(
		mbuf->nb_segs,
		(uint16_t)2,
		"strip path must keep the segment chain"
	);
	TEST_ASSERT(
		frame_header_matches(mbuf, RTE_ETHER_TYPE_IPV4),
		"strip path must expose the ethertype the tag held in a split "
		"frame"
	);
	free_packet(result.packet);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *devs_to_load[] = {"vlan"};
	struct dataplane_ut_config ut_cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&ut_cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new failed");
	struct yanet_shm *shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm failed");

	yanet_error *err = NULL;
	struct agent *agent = agent_attach(shm, 0, "vlan-test", 1u << 22, &err);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// One vlan device per output tag, created through the control-plane
	// API so the handler sees the published configuration shape.
	struct cp_device_vlan_config *untagged_cfg =
		cp_device_vlan_config_new("vlan-untagged", 1, 1, 0, &err);
	TEST_ASSERT_NOT_NULL(untagged_cfg, "untagged config new failed");
	struct cp_device *untagged_device =
		cp_device_vlan_new(agent, untagged_cfg, &err);
	cp_device_vlan_config_free(untagged_cfg);
	TEST_ASSERT_NOT_NULL(untagged_device, "untagged device new failed");

	struct cp_device_vlan_config *tagged_cfg = cp_device_vlan_config_new(
		"vlan-tagged", 1, 1, VLAN_TEST_VLAN, &err
	);
	TEST_ASSERT_NOT_NULL(tagged_cfg, "tagged config new failed");
	struct cp_device *tagged_device =
		cp_device_vlan_new(agent, tagged_cfg, &err);
	cp_device_vlan_config_free(tagged_cfg);
	TEST_ASSERT_NOT_NULL(tagged_device, "tagged device new failed");

	memset(&untagged_ectx, 0, sizeof(untagged_ectx));
	untagged_ectx.abs_cp_device = untagged_device;
	memset(&tagged_ectx, 0, sizeof(tagged_ectx));
	tagged_ectx.abs_cp_device = tagged_device;

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	uint64_t device_idx;
	TEST_ASSERT_EQUAL(
		dp_config_lookup_device(dp_config, "vlan", &device_idx),
		0,
		"the vlan device type must be loaded"
	);
	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);
	test_output_handler = dp_devices[device_idx].output_handler;
	TEST_ASSERT_NOT_NULL(
		test_output_handler, "vlan device type has no output handler"
	);

	int res = run_strip_truncated_tag_test();
	if (res == TEST_SUCCESS) {
		res = run_strip_tagged_boundary_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_retag_truncated_tag_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_retag_tagged_boundary_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_untagged_passthrough_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_inject_tag_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_strip_split_tag_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_retag_split_tag_test();
	}
	if (res == TEST_SUCCESS) {
		res = run_strip_chained_tag_in_head_test();
	}

	TEST_ASSERT_EQUAL(
		cp_device_vlan_free(untagged_device, &err),
		0,
		"a dangling untagged device must destroy on release"
	);
	TEST_ASSERT_EQUAL(
		cp_device_vlan_free(tagged_device, &err),
		0,
		"a dangling tagged device must destroy on release"
	);

	agent_detach(agent);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
