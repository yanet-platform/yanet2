#include "config.h"

#include <stdio.h>

#include "common/container_of.h"

#include "lib/dataplane/device/device.h"

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/dataplane/pipeline/pipeline.h"

// Both directions are pure pass-through.
static void
plain_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_list *input,
	struct packet_list *output,
	struct packet_list *drop
) {
	(void)dp_worker;
	(void)device_ectx;
	(void)drop;

	packet_list_concat(output, input);
}

static void
plain_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_list *input,
	struct packet_list *output,
	struct packet_list *drop
) {
	(void)dp_worker;
	(void)device_ectx;
	(void)drop;

	packet_list_concat(output, input);
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
