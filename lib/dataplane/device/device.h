#pragma once

#define DEVICE_TYPE_LEN 80

struct packet_front;
struct device_ectx;
struct dp_worker;

typedef void (*device_handler)(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
);

struct device {
	char name[DEVICE_TYPE_LEN];
	device_handler input_handler;
	device_handler output_handler;
};

typedef struct device *(*device_load_handler)();
