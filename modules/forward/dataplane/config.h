#pragma once

#include <lib/classify/classify.h>

struct classifier;

#include "lib/controlplane/config/cp_module.h"

#define FORWARD_MODE_NONE 0
#define FORWARD_MODE_IN 1
#define FORWARD_MODE_OUT 2

struct forward_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t mode;
};

struct forward_module_config {
	struct cp_module cp_module;

	struct classify_filter filter_ip4;
	struct classify_filter filter_ip6;
	struct classify_filter filter_vlan;
	// Control plane only: the classifier trees the filters borrow their
	// tapes from, released after the filters at config destroy.
	struct classifier *classifier_ip4;
	struct classifier *classifier_ip6;
	struct classifier *classifier_vlan;

	uint64_t target_count;
	struct forward_target *targets;
};
