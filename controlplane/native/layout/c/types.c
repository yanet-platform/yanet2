// The structures the native control plane's views are generated from.
//
// The file is compiled as the dataplane compiles its headers, with the
// project's arguments and DPDK's configuration, and its debug information
// keeps every structure the headers define: shmgen reads the layouts the
// views are generated from out of it, as it would out of the dataplane.

#include <rte_mbuf.h>

#include "common/value.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/rule_table.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "modules/decap/dataplane/config.h"

int
main(void) {
	return 0;
}
