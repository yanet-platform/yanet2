#pragma once

#include "lib/controlplane/config/cp_module.h"

#include "objects/classifier/api/classifier_object.h"

#define MIRROR_MODE_NONE 0
#define MIRROR_MODE_IN 1
#define MIRROR_MODE_OUT 2

// Sentinel for "no object link at this slot", mirroring the acl
// module's map link slots.
#define MIRROR_CLASSIFIER_LINK_NONE (uint64_t)-1

// The decision a classification result resolves to, compiled from the
// authored decision of the same rule index.
struct mirror_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t mode;
};

/*
 * Per-worker handles of the module's binding to the classifier
 * object: the linked object's prepared classification handles, and
 * the class of the executing device in the object's device
 * attribute, both resolved once per execution context by the commit
 * below. A context whose link resolved nothing keeps a NULL handle
 * and classifies nothing: every packet passes through unmirrored.
 */
struct mirror_prepared {
	struct classifier_prepared *classifier;
	uint32_t device_class;
};

/*
 * The module config: the decisions the classification results resolve
 * into, and the link to the classifier object that classifies.
 *
 * The classification itself - the compiled attribute classifiers and
 * their joins - is owned by the linked classifier object; the module
 * holds no filter of its own.
 */
struct mirror_module_config {
	struct cp_module cp_module;

	uint64_t classifier_link_idx;

	uint64_t target_count;
	struct mirror_target *targets;
};
