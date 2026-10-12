#pragma once

#include <stdint.h>

#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classify.h"
#include "lib/classify/rule.h"

#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/defines.h"

#include "lib/dataplane/object/object.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

#define CLASSIFIER_OBJECT_TYPE "classifier"

/*
 * The bundle one object instance compiles: a subtype names the attribute
 * classifiers of the bundle, and every create API compiles exactly one.
 *
 * The subtypes come from the classifier sets the modules compiled
 * in-house before the object existed: the shared device and vlan core
 * of the l2 paths, and the same core with both network families and
 * their family root joints of the l3 ones.
 */
enum classifier_object_subtype {
	// The zero value of a fresh buffer: no bundle selected yet.
	CLASSIFIER_OBJECT_SUBTYPE_NONE = 0,
	// The device and vlan attributes joined into the l2 classes.
	CLASSIFIER_OBJECT_SUBTYPE_L2,
	// The l2 core with the net4 and the net6 family projections
	// joined onto it through their root joints.
	CLASSIFIER_OBJECT_SUBTYPE_L3,
};

struct agent;
struct config_gen_ectx;
struct packet;

/*
 * The rule of the object's ruleset: the filter fields every subtype
 * compiles over, and nothing else. A rule carries no decision - the
 * modules linking the object resolve the classification result, a rule
 * index of this ruleset, into their own decisions.
 */
struct classifier_object_rule {
	struct classifier_rule rule;

	struct filter_devices devices;
	// The VLAN line intervals widened out of the authored VLAN
	// ranges by the control plane that authors the rules.
	struct classify_line_ranges vlan_ranges;

	struct filter_net4s src_net4s;
	struct filter_net4s dst_net4s;

	struct filter_net6s src_net6s;
	struct filter_net6s dst_net6s;
};

// One device name of the object's own device table: the object compiles
// its device attribute over these table indices the way a module
// compiles its over the module device links.
struct classifier_object_device {
	char name[CP_DEVICE_NAME_LEN];
};

/*
 * Shared core classifier: the device and vlan attributes joined once
 * over the union of the family projections; the family filters and the
 * l2 filter reference its classes.
 *
 * The attributes and the joint are embedded by value and released by
 * the object destroy through the typed attribute frees.
 */
struct cl_classifier_core {
	struct classify_attr_device dev_attr;
	struct classify_attr_line vlan_attr;
	struct value_table joint; // device classes joined with vlan classes
};

struct cl_classifier_net4 {
	struct classify_attr_net4 src_attr;
	struct classify_attr_net4 dst_attr;
	struct value_table joint;
};

struct cl_classifier_net6 {
	struct classify_attr_net6 src_attr;
	struct classify_attr_net6 dst_attr;
	struct value_table joint;
};

/*
 * Final filters: the l2 decoder over the core classes, and the family
 * root joints joining the core classes with the network pair classes,
 * each with its own projection decoder.
 */
struct cl_filter_l2 {
	struct vline rule_map;
};

struct cl_filter_ip4 {
	struct value_table root_joint;
	struct vline rule_map;
};

struct cl_filter_ip6 {
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * The classifier object: a ruleset compiled into the bundle its
 * subtype names, owned by the object's memory context.
 *
 * The device table is the object's own: the rules address devices by
 * name, the compile assigns each name its table index, and the device
 * attribute classes are read through that table - a module linking the
 * object asks for its executing device's class by name.
 */
struct classifier_object {
	struct cp_object cp_object;

	enum classifier_object_subtype subtype;

	uint64_t device_count;
	struct classifier_object_device *devices;

	uint32_t rule_count;

	struct cl_classifier_core core;
	struct cl_classifier_net4 net4;
	struct cl_classifier_net6 net6;

	struct cl_filter_l2 filter_l2;
	struct cl_filter_ip4 filter_ip4;
	struct cl_filter_ip6 filter_ip6;
};

// Batch classify routine of one subtype: resolves every packet of the
// batch to a rule index of the object's ruleset, or to
// CLASSIFY_RULE_INVALID when no rule matches.
//
// device_class is the class of the executing device in the object's
// device attribute, resolved by the calling module through
// classifier_object_device_class once per its execution context.
typedef void (*classifier_object_classify_batch_t)(
	const struct classifier_object *object,
	uint32_t device_class,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
);

/*
 * The object's private per-context buffer: the classification handles
 * the object's subtype selects.
 *
 * The commit pass fills it once per object execution context: the
 * subtype names the batch routine of the object's bundle, and the
 * public handler below dispatches through the installed handle.
 */
struct classifier_prepared {
	// The subtype the handle below was selected for; kept beside the
	// handle so a buffer of an unselected or unknown subtype carries
	// a NULL handle and classifies nothing.
	enum classifier_object_subtype subtype;

	// The compiled bundle of the object, absolute; the routine below
	// classifies through it.
	const struct classifier_object *object;

	// The batch classify routine of the subtype, NULL when the
	// subtype selects none: the public handler then resolves every
	// packet to no rule.
	classifier_object_classify_batch_t classify_batch;
};

/*
 * Public handler of the classifier object type, well defined by the
 * type alone: classifies a batch of packets into rule indices of the
 * linked object's ruleset.
 *
 * The handler checks the subtype selection of the buffer - the handle
 * the object's subtype installed - and runs the classification of that
 * subtype over the batch. A buffer without a selected handle resolves
 * every packet to CLASSIFY_RULE_INVALID.
 */
static inline void
classifier_object_classify(
	const struct classifier_prepared *prepared,
	uint32_t device_class,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	if (prepared->classify_batch == NULL) {
		for (uint32_t idx = 0; idx < count; ++idx) {
			results[idx] = CLASSIFY_RULE_INVALID;
		}
		return;
	}

	prepared->classify_batch(
		prepared->object, device_class, packets, results, count
	);
}

/*
 * Create APIs, one per known subtype: each compiles the ruleset into
 * the bundle its subtype names and returns the object handle ready for
 * agent_update_objects.
 *
 * The rules are caller-owned scratch: the compile writes the assigned
 * device table indices back into the device items, like a module
 * update does through its device links. rule_count may be zero.
 *
 * The l2 create rejects rules carrying network conditions: the l2
 * bundle classifies through the device and vlan attributes alone. The
 * l3 create takes the full field set, the rules without network
 * conditions projecting to the l2 path.
 *
 * Returns NULL with err set on failure; nothing is left allocated.
 */
struct cp_object *
classifier_object_config_new_l2(
	struct agent *agent,
	const char *name,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
);

struct cp_object *
classifier_object_config_new_l3(
	struct agent *agent,
	const char *name,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
);

// Destroy the object when it is dangling, per cp_object_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the object; the caller must keep its handle and retry later.
int
classifier_object_config_free(struct cp_object *cp_object, yanet_error **err);

// The subtype the object was created with.
enum classifier_object_subtype
classifier_object_subtype(const struct cp_object *cp_object);

// The rule count of the object's ruleset: the valid range of the
// classification results the object yields.
uint32_t
classifier_object_rule_count(const struct cp_object *cp_object);

// The size of the object's own device table.
uint64_t
classifier_object_device_count(const struct cp_object *cp_object);

// The device name at the given table index; indices are validated by
// the caller against classifier_object_device_count.
const char *
classifier_object_device_name(const struct cp_object *cp_object, uint64_t idx);

/*
 * The class of the named device in the object's device attribute.
 *
 * A device the rules never named resolves to the class of the any
 * device slot, mirroring the module device attribute's treatment of an
 * executing device the module never linked: the slot keeps the class
 * the empty device set compiled to.
 */
uint32_t
classifier_object_device_class(
	const struct cp_object *cp_object, const char *device_name
);

/*
 * Compiles the ruleset into the bundle the subtype names, and
 * releases a compiled bundle: the contextless-consumer entries the
 * create APIs and the destroy wrap.
 *
 * The compile requires the embedded cp_object's memory context
 * initialized and nothing else, so a consumer without an agent - a
 * fuzzing harness building the object by hand - may zero the struct,
 * init the memory context and run the compile directly; the release
 * is safe on a partially built object of a failed compile.
 */
int
classifier_object_compile(
	struct classifier_object *self,
	enum classifier_object_subtype subtype,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
);

void
classifier_object_fini_data(struct classifier_object *self);

/*
 * Select the classification handles of the object's subtype into a
 * prepared buffer.
 *
 * The object's execution-context commit handler runs this for every
 * per-worker buffer the control plane allocates; a contextless
 * consumer - a fuzzing harness building the object by hand - may run
 * it directly. The buffer is rewritten whole, so a repeated run
 * re-derives the same handles.
 */
void
classifier_object_prepare(
	struct classifier_prepared *prepared,
	const struct classifier_object *object
);

// The dataplane object-type constructor, resolved by dp_load_object
// from the main binary: it declares the type's execution-context
// commit handler and prepared buffer size.
struct object *
new_object_classifier(void);
