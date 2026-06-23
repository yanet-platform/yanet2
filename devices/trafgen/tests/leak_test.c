#include "common/test_assert.h"
#include "devices/trafgen/api/controlplane.h"
#include "lib/errors/errors.h"

int
main(void) {
	yanet_error *err = NULL;

	struct cp_device_trafgen_config *cfg =
		cp_device_trafgen_config_new("test", 4, 4, &err);
	TEST_ASSERT_NOT_NULL(cfg, "cp_device_trafgen_config_new returned NULL");
	TEST_ASSERT_NULL(
		err, "unexpected error from cp_device_trafgen_config_new"
	);

	int res = cp_device_trafgen_config_set_input_pipeline(cfg, 0, "p0", 1);
	TEST_ASSERT_EQUAL(
		res, 0, "cp_device_trafgen_config_set_input_pipeline failed"
	);

	res = cp_device_trafgen_config_set_output_pipeline(cfg, 0, "p0", 1);
	TEST_ASSERT_EQUAL(
		res, 0, "cp_device_trafgen_config_set_output_pipeline failed"
	);

	cp_device_trafgen_config_free(cfg);

	return 0;
}
