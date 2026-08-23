/*
 * Dataplane coverage for the vxlan device input and output handlers.
 *
 * Permanent C tests because these cases are unreachable from Go. The
 * harness packet builders hardwire data_off to RTE_PKTMBUF_HEADROOM (256)
 * and expose no headroom control, so the 50-byte encap prepend can never
 * fail from Go. And no Go-loadable module reads the transport header of
 * a non-IP-typed packet or the vlan id of an untagged frame, so the decap
 * resets of the stale outer UDP transport metadata and VLAN id cannot be
 * asserted there.
 */

#include <stdint.h>
#include <string.h>

#include <rte_arp.h>
#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_udp.h>
#include <rte_vxlan.h>

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "devices/vxlan/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/utils/packet.h"

#define INNER_PAYLOAD_LEN 32

// The VXLAN I flag the input handler requires in the outer header.
#define TEST_VXLAN_FLAGS 0x08

static struct cp_device_vxlan_settings test_settings = {
	.vni = 4242,
	.dst_port = 4789,
	.src_mac = {0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
	.dst_mac = {0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
	.src_ip = 0,
	.dst_ip = 0,
};

static struct device_ectx test_ectx;

// Ethernet + IPv4 + UDP + payload, the frame a tunnel carries inside.
static uint16_t
build_inner_frame(uint8_t *frame) {
	uint16_t offset = 0;

	struct rte_ether_hdr *ether_hdr = (struct rte_ether_hdr *)frame;
	memset(ether_hdr, 0xab, sizeof(*ether_hdr));
	ether_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	offset += sizeof(*ether_hdr);

	struct rte_ipv4_hdr *ipv4_hdr = (struct rte_ipv4_hdr *)(frame + offset);
	ipv4_hdr->version_ihl = 0x45;
	ipv4_hdr->total_length = rte_cpu_to_be_16(
		sizeof(*ipv4_hdr) + sizeof(struct rte_udp_hdr) +
		INNER_PAYLOAD_LEN
	);
	ipv4_hdr->time_to_live = 64;
	ipv4_hdr->next_proto_id = IPPROTO_UDP;
	// Unsigned octets keep RTE_IPV4's shifts unsigned. 192 << 24
	// overflows int and UBSan halts on it.
	ipv4_hdr->src_addr = rte_cpu_to_be_32(RTE_IPV4(192u, 168u, 1u, 1u));
	ipv4_hdr->dst_addr = rte_cpu_to_be_32(RTE_IPV4(192u, 168u, 1u, 2u));
	offset += sizeof(*ipv4_hdr);

	struct rte_udp_hdr *udp_hdr = (struct rte_udp_hdr *)(frame + offset);
	udp_hdr->src_port = rte_cpu_to_be_16(1234);
	udp_hdr->dst_port = rte_cpu_to_be_16(5678);
	udp_hdr->dgram_len =
		rte_cpu_to_be_16(sizeof(*udp_hdr) + INNER_PAYLOAD_LEN);
	offset += sizeof(*udp_hdr);

	for (uint16_t idx = 0; idx < INNER_PAYLOAD_LEN; ++idx) {
		frame[offset + idx] = (uint8_t)(idx * 7 + 3);
	}
	offset += INNER_PAYLOAD_LEN;

	return offset;
}

// Ethernet + ARP, a non-IP frame a tunnel can carry inside.
static uint16_t
build_arp_inner_frame(uint8_t *frame) {
	uint16_t offset = 0;

	struct rte_ether_hdr *ether_hdr = (struct rte_ether_hdr *)frame;
	memset(ether_hdr, 0xcd, sizeof(*ether_hdr));
	ether_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_ARP);
	offset += sizeof(*ether_hdr);

	struct rte_arp_hdr *arp_hdr = (struct rte_arp_hdr *)(frame + offset);
	memset(arp_hdr, 0x5a, sizeof(*arp_hdr));
	arp_hdr->arp_hardware = rte_cpu_to_be_16(RTE_ARP_HRD_ETHER);
	arp_hdr->arp_protocol = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	arp_hdr->arp_hlen = RTE_ETHER_ADDR_LEN;
	arp_hdr->arp_plen = 4;
	arp_hdr->arp_opcode = rte_cpu_to_be_16(RTE_ARP_OP_REQUEST);
	offset += sizeof(*arp_hdr);

	return offset;
}

// Ethernet + IPv4 + UDP + VXLAN around a carried frame, with every
// declared length covering the full inner so the envelope checks pass.
static uint16_t
build_vxlan_outer_frame(
	uint8_t *frame, const uint8_t *inner, uint16_t inner_len
) {
	uint16_t offset = 0;

	uint16_t dgram_len = sizeof(struct rte_udp_hdr) +
			     sizeof(struct rte_vxlan_hdr) + inner_len;

	struct rte_ether_hdr *ether_hdr = (struct rte_ether_hdr *)frame;
	memset(ether_hdr, 0xab, sizeof(*ether_hdr));
	ether_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	offset += sizeof(*ether_hdr);

	struct rte_ipv4_hdr *ipv4_hdr = (struct rte_ipv4_hdr *)(frame + offset);
	memset(ipv4_hdr, 0, sizeof(*ipv4_hdr));
	ipv4_hdr->version_ihl = 0x45;
	ipv4_hdr->total_length =
		rte_cpu_to_be_16((uint16_t)sizeof(*ipv4_hdr) + dgram_len);
	ipv4_hdr->time_to_live = 64;
	ipv4_hdr->next_proto_id = IPPROTO_UDP;
	// Unsigned octets keep RTE_IPV4's shifts unsigned. 192 << 24
	// overflows int and UBSan halts on it.
	ipv4_hdr->src_addr = rte_cpu_to_be_32(RTE_IPV4(192u, 168u, 1u, 1u));
	ipv4_hdr->dst_addr = rte_cpu_to_be_32(RTE_IPV4(192u, 168u, 1u, 2u));
	offset += sizeof(*ipv4_hdr);

	struct rte_udp_hdr *udp_hdr = (struct rte_udp_hdr *)(frame + offset);
	memset(udp_hdr, 0, sizeof(*udp_hdr));
	udp_hdr->src_port = rte_cpu_to_be_16(1234);
	udp_hdr->dst_port = rte_cpu_to_be_16(test_settings.dst_port);
	udp_hdr->dgram_len = rte_cpu_to_be_16(dgram_len);
	offset += sizeof(*udp_hdr);

	struct rte_vxlan_hdr *vxlan_hdr =
		(struct rte_vxlan_hdr *)(frame + offset);
	vxlan_hdr->vx_flags =
		rte_cpu_to_be_32((uint32_t)TEST_VXLAN_FLAGS << 24);
	vxlan_hdr->vx_vni = rte_cpu_to_be_32((uint32_t)test_settings.vni << 8);
	offset += sizeof(*vxlan_hdr);

	memcpy(frame + offset, inner, inner_len);
	offset += inner_len;

	return offset;
}

static struct rte_mbuf *
frame_mbuf(uint16_t headroom, const uint8_t *frame, uint16_t frame_len) {
	struct rte_mbuf *mbuf = alloc_mbuf(headroom, frame_len, 0);
	if (mbuf != NULL && frame != NULL) {
		memcpy(rte_pktmbuf_mtod(mbuf, void *), frame, frame_len);
	}
	return mbuf;
}

static void
init_packet(struct packet *packet, struct rte_mbuf *mbuf) {
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
}

// Run one handler over one packet and leave the result lists in pf.
static void
run_handler(
	device_handler handler, struct packet *packet, struct packet_front *pf
) {
	packet_front_init(pf);
	packet_front_input(pf, packet);
	handler(NULL, &test_ectx, pf);
}

static void
free_result(struct packet_front *pf) {
	struct packet *packet;
	while ((packet = packet_list_pop(&pf->output)) != NULL) {
		free_packet(packet);
	}
	while ((packet = packet_list_pop(&pf->drop)) != NULL) {
		free_packet(packet);
	}
}

// Encapsulation prepends 50 outer bytes into the mbuf headroom, so a
// packet with less headroom than that must be dropped, never emitted.
static int
run_encap_headroom_drop_test(device_handler handler) {
	uint8_t inner[128];
	uint16_t inner_len = build_inner_frame(inner);

	struct packet packet;
	struct rte_mbuf *mbuf = frame_mbuf(16, inner, inner_len);
	TEST_ASSERT_NOT_NULL(mbuf, "alloc_mbuf returned NULL");
	init_packet(&packet, mbuf);

	struct packet_front pf;
	run_handler(handler, &packet, &pf);
	TEST_ASSERT_EQUAL(
		(long)packet_front_output_count(&pf),
		0L,
		"a frame without headroom must not be encapsulated"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_front_drop_count(&pf),
		1L,
		"a frame without headroom must be dropped"
	);
	free_result(&pf);

	return TEST_SUCCESS;
}

// A decapsulated non-IP inner frame must not keep the outer UDP
// transport header the arrival parse left behind: parse_packet returns
// at the unknown ether type without touching transport metadata, so only
// the explicit reset restores the fresh-packet state.
static int
run_decap_transport_reset_test(device_handler handler) {
	uint8_t inner[128];
	uint16_t inner_len = build_arp_inner_frame(inner);
	uint8_t outer[128];
	uint16_t outer_len = build_vxlan_outer_frame(outer, inner, inner_len);

	struct packet packet;
	struct rte_mbuf *mbuf =
		frame_mbuf(RTE_PKTMBUF_HEADROOM, outer, outer_len);
	TEST_ASSERT_NOT_NULL(mbuf, "alloc_mbuf returned NULL");
	init_packet(&packet, mbuf);

	// Seed the metadata a real RX parse of the outer frame leaves —
	// worker.c runs parse_packet on arrival — so the stale transport
	// header under test is exactly the production one, outer UDP type
	// and offset included.
	TEST_ASSERT_EQUAL(
		parse_packet(&packet), 0, "the outer frame must parse"
	);
	TEST_ASSERT_EQUAL(
		(long)packet.transport_header.type,
		(long)IPPROTO_UDP,
		"the arrival parse must leave the outer UDP transport type"
	);
	TEST_ASSERT_EQUAL(
		(long)packet.transport_header.offset,
		(long)(sizeof(struct rte_ether_hdr) +
		       sizeof(struct rte_ipv4_hdr)),
		"the arrival parse must leave the outer UDP transport offset"
	);

	struct packet_front pf;
	run_handler(handler, &packet, &pf);

	TEST_ASSERT_EQUAL(
		(long)packet_front_output_count(&pf),
		1L,
		"a valid vxlan frame carrying ARP must be decapsulated"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_front_drop_count(&pf),
		0L,
		"a valid vxlan frame carrying ARP must not be dropped"
	);

	struct packet *decapped = packet_list_pop(&pf.output);
	TEST_ASSERT_NOT_NULL(decapped, "the output list must hold the packet");
	TEST_ASSERT(
		decapped->transport_header.type == PACKET_HEADER_TYPE_UNKNOWN &&
			decapped->transport_header.offset == 0,
		"a non-IP inner frame must not keep the outer transport "
		"header {type, offset} (expected {0, 0}, got {%u, %u})",
		decapped->transport_header.type,
		decapped->transport_header.offset
	);

	struct rte_mbuf *decapped_mbuf = packet_to_mbuf(decapped);
	TEST_ASSERT_EQUAL(
		(long)rte_pktmbuf_data_len(decapped_mbuf),
		(long)inner_len,
		"the decapsulated frame length must equal the inner length"
	);
	TEST_ASSERT_EQUAL(
		memcmp(rte_pktmbuf_mtod(decapped_mbuf, const void *),
		       inner,
		       inner_len),
		0,
		"the decapsulated bytes must equal the inner frame"
	);

	free_packet(decapped);
	free_result(&pf);

	return TEST_SUCCESS;
}

// A matching frame that reaches the handler after a vlan device stripped
// its outer tag carries the removed VLAN id in the metadata while the
// bytes are untagged, and parse_packet leaves the id for an untagged inner
// frame: only the explicit reset restores the fresh-packet state.
static int
run_decap_vlan_reset_test(device_handler handler) {
	uint8_t inner[128];
	uint16_t inner_len = build_inner_frame(inner);
	uint8_t outer[192];
	uint16_t outer_len = build_vxlan_outer_frame(outer, inner, inner_len);

	struct packet packet;
	struct rte_mbuf *mbuf =
		frame_mbuf(RTE_PKTMBUF_HEADROOM, outer, outer_len);
	TEST_ASSERT_NOT_NULL(mbuf, "alloc_mbuf returned NULL");
	init_packet(&packet, mbuf);

	// Seed the metadata a real RX parse of the outer frame leaves.
	TEST_ASSERT_EQUAL(
		parse_packet(&packet), 0, "the outer frame must parse"
	);
	// A vlan device configured for the tag strips it from the bytes
	// without touching the parsed metadata, which is how a packet
	// arrives at the vxlan handler with a vlan id over untagged IPv4.
	packet.vlan = 100;

	struct packet_front pf;
	run_handler(handler, &packet, &pf);

	TEST_ASSERT_EQUAL(
		(long)packet_front_output_count(&pf),
		1L,
		"a valid vxlan frame must be decapsulated"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_front_drop_count(&pf),
		0L,
		"a valid vxlan frame must not be dropped"
	);

	struct packet *decapped = packet_list_pop(&pf.output);
	TEST_ASSERT_NOT_NULL(decapped, "the output list must hold the packet");
	TEST_ASSERT_EQUAL(
		(long)decapped->vlan,
		0L,
		"an untagged inner frame must not keep the stripped outer "
		"vlan id (expected 0, got %u)",
		decapped->vlan
	);

	free_packet(decapped);
	free_result(&pf);

	return TEST_SUCCESS;
}

// A single-segment frame the total-length check accepts can still be too
// large for the head segment: prepending 50 bytes would overflow its 16-bit
// data_len, which the headroom check inside rte_pktmbuf_prepend does not
// guard, so the handler must drop it instead of wrapping the field.
static int
run_encap_head_overflow_drop_test(device_handler handler) {
	// The largest frame the total-length check accepts (65535 minus the
	// IPv4+UDP+VXLAN overhead): its head segment plus the 50-byte
	// prepend exceeds 16 bits.
	uint16_t overflow_len = UINT16_MAX - (sizeof(struct rte_ipv4_hdr) +
					      sizeof(struct rte_udp_hdr) +
					      sizeof(struct rte_vxlan_hdr));

	struct packet packet;
	struct rte_mbuf *mbuf =
		frame_mbuf(RTE_PKTMBUF_HEADROOM, NULL, overflow_len);
	TEST_ASSERT_NOT_NULL(mbuf, "alloc_mbuf returned NULL");
	init_packet(&packet, mbuf);

	struct packet_front pf;
	run_handler(handler, &packet, &pf);
	TEST_ASSERT_EQUAL(
		(long)packet_front_output_count(&pf),
		0L,
		"a frame whose head segment would overflow must not be "
		"encapsulated"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_front_drop_count(&pf),
		1L,
		"a frame whose head segment would overflow must be dropped"
	);
	free_result(&pf);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	// The physical port resolves to the plain device type, so plain has
	// to be loaded alongside vxlan.
	const char *devs_to_load[] = {"plain", "vxlan"};

	struct dataplane_ut_config ut_cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 2,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&ut_cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");
	struct yanet_shm *shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");

	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "vxlan-test", 1u << 22, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_device_vxlan_config *cfg =
		cp_device_vxlan_config_new("tun0", 1, 1, &test_settings, &err);
	TEST_ASSERT_NOT_NULL(cfg, "cp_device_vxlan_config_new returned NULL");

	struct cp_device *cp_device = cp_device_vxlan_new(agent, cfg, &err);
	TEST_ASSERT_NOT_NULL(cp_device, "cp_device_vxlan_new returned NULL");
	cp_device_vxlan_config_free(cfg);

	// The handler runs against this shared-memory configuration the
	// same way device_ectx wiring presents it in a live generation.
	memset(&test_ectx, 0, sizeof(test_ectx));
	SET_OFFSET_OF(&test_ectx.cp_device, cp_device);

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	uint64_t device_idx;
	TEST_ASSERT_EQUAL(
		dp_config_lookup_device(dp_config, "vxlan", &device_idx),
		0,
		"the vxlan device type must be loaded"
	);
	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);
	device_handler input_handler = dp_devices[device_idx].input_handler;
	TEST_ASSERT_NOT_NULL(
		input_handler, "vxlan device type has no input handler"
	);
	device_handler output_handler = dp_devices[device_idx].output_handler;
	TEST_ASSERT_NOT_NULL(
		output_handler, "vxlan device type has no output handler"
	);

	int res = run_encap_headroom_drop_test(output_handler);
	if (res == TEST_SUCCESS) {
		res = run_decap_transport_reset_test(input_handler);
	}
	if (res == TEST_SUCCESS) {
		res = run_decap_vlan_reset_test(input_handler);
	}
	if (res == TEST_SUCCESS) {
		res = run_encap_head_overflow_drop_test(output_handler);
	}

	size_t after_create =
		block_allocator_free_size(&agent->block_allocator);

	// Releasing the device parks it on the agent instead of destroying
	// it, so the next construction must reclaim the parked entry whole:
	// its free bytes then equal the first construction's, and repeated
	// create/release cycles cannot exhaust the agent memory.
	cp_device_vxlan_free(cp_device);

	struct cp_device_vxlan_config *reclaim_cfg =
		cp_device_vxlan_config_new("tun1", 1, 1, &test_settings, &err);
	TEST_ASSERT_NOT_NULL(
		reclaim_cfg, "cp_device_vxlan_config_new returned NULL"
	);
	struct cp_device *reclaimed =
		cp_device_vxlan_new(agent, reclaim_cfg, &err);
	TEST_ASSERT_NOT_NULL(reclaimed, "cp_device_vxlan_new returned NULL");
	cp_device_vxlan_config_free(reclaim_cfg);

	size_t after_reclaim =
		block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after_reclaim,
		(long)after_create,
		"a parked device must return every arena byte on reclaim: "
		"after_create=%zu after_reclaim=%zu",
		after_create,
		after_reclaim
	);
	cp_device_vxlan_free(reclaimed);

	agent_detach(agent);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
