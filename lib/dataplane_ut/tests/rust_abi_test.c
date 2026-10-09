// Cross-checks the Rust SDK's struct mirrors against the real C
// layouts, in the same binary: the yanet-dp crate exports its compiled
// sizes and offsets as yanet_dp_* functions, and any divergence between
// the two sides of the module ABI fails here instead of corrupting
// shared memory at runtime.

#include <stddef.h>
#include <stdint.h>

#include <rte_mbuf.h>

#include "common/lpm.h"
#include "common/memory.h"
#include "common/test_assert.h"
#include "common/value.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/plugin_abi_assert.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/filter/filter.h"

size_t
yanet_dp_sizeof_module(void);
size_t
yanet_dp_sizeof_packet(void);
size_t
yanet_dp_sizeof_packet_front(void);
size_t
yanet_dp_sizeof_module_ectx(void);
size_t
yanet_dp_sizeof_dp_worker(void);
size_t
yanet_dp_sizeof_rte_mbuf(void);
size_t
yanet_dp_sizeof_cp_module(void);
size_t
yanet_dp_sizeof_memory_context(void);
size_t
yanet_dp_sizeof_lpm(void);
size_t
yanet_dp_sizeof_value_table(void);
size_t
yanet_dp_sizeof_vline(void);
size_t
yanet_dp_sizeof_filter(void);

size_t
yanet_dp_offset_packet_mbuf(void);
size_t
yanet_dp_offset_packet_data_len(void);
size_t
yanet_dp_offset_rte_mbuf_next(void);
size_t
yanet_dp_offset_rte_mbuf_buf_addr(void);
size_t
yanet_dp_offset_rte_mbuf_data_off(void);
size_t
yanet_dp_offset_rte_mbuf_pkt_len(void);
size_t
yanet_dp_offset_rte_mbuf_data_len(void);
size_t
yanet_dp_offset_module_ectx_abs_cp_module(void);
size_t
yanet_dp_offset_module_ectx_abs_counter_storage(void);
size_t
yanet_dp_offset_module_ectx_packet_recirc_limit(void);
size_t
yanet_dp_offset_module_ectx_abs_module_prepared(void);
size_t
yanet_dp_offset_device_entry_ectx_schedule(void);
size_t
yanet_dp_offset_config_gen_ectx_ready_list(void);
size_t
yanet_dp_offset_dp_worker_current_time(void);
uint32_t
yanet_dp_abi_version(void);

int
main(void) {
	TEST_ASSERT_EQUAL(
		(long)YANET_MODULE_ABI_VERSION,
		(long)yanet_dp_abi_version(),
		"yanet-dp ABI version mirror diverged from module.h"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct module),
		(long)yanet_dp_sizeof_module(),
		"struct module mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct packet),
		(long)yanet_dp_sizeof_packet(),
		"struct packet mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct packet_front),
		(long)yanet_dp_sizeof_packet_front(),
		"struct packet_front mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct module_ectx),
		(long)yanet_dp_sizeof_module_ectx(),
		"struct module_ectx mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct dp_worker),
		(long)yanet_dp_sizeof_dp_worker(),
		"struct dp_worker mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct rte_mbuf),
		(long)yanet_dp_sizeof_rte_mbuf(),
		"rte_mbuf mirror size diverged; check the DPDK build's IOVA "
		"shape"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct cp_module),
		(long)yanet_dp_sizeof_cp_module(),
		"struct cp_module mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct memory_context),
		(long)yanet_dp_sizeof_memory_context(),
		"struct memory_context mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct lpm),
		(long)yanet_dp_sizeof_lpm(),
		"struct lpm mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct value_table),
		(long)yanet_dp_sizeof_value_table(),
		"struct value_table mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct vline),
		(long)yanet_dp_sizeof_vline(),
		"struct vline mirror size diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)sizeof(struct filter),
		(long)yanet_dp_sizeof_filter(),
		"struct filter mirror size diverged"
	);

	TEST_ASSERT_EQUAL(
		(long)offsetof(struct packet, mbuf),
		(long)yanet_dp_offset_packet_mbuf(),
		"packet.mbuf mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct packet, data_len),
		(long)yanet_dp_offset_packet_data_len(),
		"packet.data_len mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct rte_mbuf, next),
		(long)yanet_dp_offset_rte_mbuf_next(),
		"rte_mbuf.next mirror offset diverged; a nonstandard "
		"RTE_IOVA_IN_MBUF build moves it into cacheline zero"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct rte_mbuf, buf_addr),
		(long)yanet_dp_offset_rte_mbuf_buf_addr(),
		"rte_mbuf.buf_addr mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct rte_mbuf, data_off),
		(long)yanet_dp_offset_rte_mbuf_data_off(),
		"rte_mbuf.data_off mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct rte_mbuf, pkt_len),
		(long)yanet_dp_offset_rte_mbuf_pkt_len(),
		"rte_mbuf.pkt_len mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct rte_mbuf, data_len),
		(long)yanet_dp_offset_rte_mbuf_data_len(),
		"rte_mbuf.data_len mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct module_ectx, abs_cp_module),
		(long)yanet_dp_offset_module_ectx_abs_cp_module(),
		"module_ectx.abs_cp_module mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct module_ectx, abs_counter_storage),
		(long)yanet_dp_offset_module_ectx_abs_counter_storage(),
		"module_ectx.abs_counter_storage mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct module_ectx, packet_recirc_limit),
		(long)yanet_dp_offset_module_ectx_packet_recirc_limit(),
		"module_ectx.packet_recirc_limit mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct module_ectx, abs_module_prepared),
		(long)yanet_dp_offset_module_ectx_abs_module_prepared(),
		"module_ectx.abs_module_prepared mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct device_entry_ectx, schedule),
		(long)yanet_dp_offset_device_entry_ectx_schedule(),
		"device_entry_ectx.schedule mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct config_gen_ectx, ready_list),
		(long)yanet_dp_offset_config_gen_ectx_ready_list(),
		"config_gen_ectx.ready_list mirror offset diverged"
	);
	TEST_ASSERT_EQUAL(
		(long)offsetof(struct dp_worker, current_time),
		(long)yanet_dp_offset_dp_worker_current_time(),
		"dp_worker.current_time mirror offset diverged"
	);

	return TEST_SUCCESS;
}
