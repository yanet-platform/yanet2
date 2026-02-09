#include "config.h"

#include "common/container_of.h"

#include "lib/dataplane/device/device.h"

#include "dataplane/packet/packet.h"

#include "dataplane/pipeline/pipeline.h"

static void
plain_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)device_ectx;

	packet_list_concat(&packet_front->output, &packet_front->input);
}

static void
plain_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)device_ectx;

	packet_list_concat(&packet_front->output, &packet_front->input);
}

struct device_plain {
	struct device device;
};

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

	return &device_plain->device;
}
