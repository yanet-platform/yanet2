#include <string.h>

#include "common/test_assert.h"
#include "devices/vxlan/api/controlplane.h"
#include "lib/errors/errors.h"

int
main(void) {
	yanet_error *err = NULL;

	struct cp_device_vxlan_settings settings = {
		.vni = 42,
		.dst_port = 4789,
		.src_mac = {0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		.dst_mac = {0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		.src_ip = 0x0100000a,
		.dst_ip = 0x0200000a,
	};

	struct cp_device_vxlan_config *cfg =
		cp_device_vxlan_config_new("test", 4, 4, &settings, &err);
	TEST_ASSERT_NOT_NULL(cfg, "cp_device_vxlan_config_new returned NULL");
	TEST_ASSERT_NULL(
		err, "unexpected error from cp_device_vxlan_config_new"
	);

	int res = cp_device_vxlan_config_set_input_pipeline(cfg, 0, "p0", 1);
	TEST_ASSERT_EQUAL(
		res, 0, "cp_device_vxlan_config_set_input_pipeline failed"
	);

	res = cp_device_vxlan_config_set_output_pipeline(cfg, 0, "p0", 1);
	TEST_ASSERT_EQUAL(
		res, 0, "cp_device_vxlan_config_set_output_pipeline failed"
	);

	cp_device_vxlan_config_free(cfg);

	return 0;
}
