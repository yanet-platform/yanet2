#include <stdio.h>

#include <dlfcn.h>
#include <string.h>

#include <pcap.h>

#include <rte_mbuf.h>

#include "dataplane/dpdk.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include <rte_common.h>
#include <rte_memcpy.h>

#include "dataplane/module/module.h"

#include "test.h"

#include "dataplane.h"

struct nat64_unittest_params {
	struct packet_front packet_front;
	struct module *module;
    struct module_config *new_module_config;

	struct rte_mempool *mbuf_pool;
};

static struct nat64_unittest_params test_params = {

	.mbuf_pool = NULL,
};

static uint8_t config_data[] = {
    0x00, 0x01, 0x02, 0x03,
    0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b,
    0x0c, 0x0d, 0x0e, 0x0f,
    0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
    0x18, 0x19, 0x1a, 0x1b,
    0x1c, 0x1d, 0x1e, 0x1f, 0x20, 0x21, 0x22, 0x23,
    0x2c, 0x2d, 0x2e, 0x2f,
    0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b
};

static int
test_setup(void) {
	const uint8_t socket_id = rte_socket_id();
	if (test_params.mbuf_pool == NULL) {
		test_params.mbuf_pool = rte_pktmbuf_pool_create(
			"TEST_NAT64",
			4096,
			250,
			0,
			RTE_MBUF_DEFAULT_BUF_SIZE,
			socket_id
		);

		TEST_ASSERT_NOT_NULL(
			test_params.mbuf_pool,
			"rte_mempool_create failed\n"
		);
	}

	packet_front_init(&test_params.packet_front);

	return TEST_SUCCESS;
}

static void
testsuite_teardown(void) {
}

static int
test_module_config_handler(void) {
        test_params.module->config_handler(test_params.module,&config_data, 80, &test_params.new_module_config);
        TEST_ASSERT_NOT_NULL(test_params.new_module_config, "module_config_handler failed\n");
    return TEST_SUCCESS;
}

static int
test_new_module_nat64(void) {
    test_params.module = new_module_nat64();
    TEST_ASSERT_NOT_NULL(test_params.module, "new_module_nat64 failed\n");
    return TEST_SUCCESS;
}

static int
test_nat64(void) {
	struct rte_mbuf *mbuf;
    return TEST_SUCCESS;
	mbuf = rte_pktmbuf_alloc(test_params.mbuf_pool);
	uint16_t len = 10;
    char* data = "lkjlkjlkj"; 
	rte_memcpy(rte_pktmbuf_mtod(mbuf, void *), data, len);
	mbuf->data_len = len;
	mbuf->pkt_len = mbuf->data_len;
	mbuf->port = 0;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;
	packet->rx_device_id = 0;
	packet->tx_device_id = 0;
	parse_packet(packet);

	packet_list_add(&test_params.packet_front.input, packet);
    return 0;
}

static struct unit_test_suite nat64_test_suite =
	{.suite_name = "NAT64 Unit Test Suite",
	 .setup = test_setup,
	 .teardown = testsuite_teardown,
	 .unit_test_cases = {
		 TEST_CASE_NAMED("test_nat64_new_module", test_new_module_nat64),
		 TEST_CASE_NAMED("test_nat64_config_handler", test_module_config_handler),
		 TEST_CASE_NAMED("test_nat64", test_nat64),

		 TEST_CASES_END() /**< NULL terminate unit test array */
	 }};

static int
nat64_testsuite(void) {
	return unit_test_suite_runner(&nat64_test_suite);
}

REGISTER_FAST_TEST(nat64_autotest, false, true, nat64_testsuite);