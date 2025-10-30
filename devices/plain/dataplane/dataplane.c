#include "config.h"

#include "common/container_of.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

static void
plain_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet *packet
) {
	(void)dp_worker;
	(void)device_ectx;
	(void)packet;
}

static void
plain_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet *packet
) {
	(void)dp_worker;
	(void)device_ectx;
	(void)packet;
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
