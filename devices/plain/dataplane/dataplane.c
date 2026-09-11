#include "config.h"

#include <stdio.h>

#include "common/container_of.h"

#include "lib/dataplane/device/device.h"

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/dataplane/pipeline/pipeline.h"

static void
plain_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)device_ectx;

	packet_front_pass(packet_front);
}

static void
plain_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)device_ectx;

	packet_front_pass(packet_front);
}

struct device_plain {
	struct device device;
};

static void
plain_device_commit(struct dp_config *dp_config, struct cp_device *cp_device) {
	(void)dp_config;
	(void)cp_device;
}

struct device *
new_device_plain() {
	struct device_plain *device_plain =
		(struct device_plain *)malloc(sizeof(struct device_plain));

	if (device_plain == NULL) {
		return NULL;
	}

	snprintf(
		device_plain->device.name,
		sizeof(device_plain->device.name),
		"%s",
		"plain"
	);
	device_plain->device.input_handler = plain_input_handle;
	device_plain->device.output_handler = plain_output_handle;
	device_plain->device.commit_handler = plain_device_commit;

	return &device_plain->device;
}
