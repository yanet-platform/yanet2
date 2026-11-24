//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <pthread.h>
#include "lib/dataplane/config/zone.h"

static void
coracle_assign_worker_ectxs(struct dp_config *dp, struct cp_config_gen *gen) {
	pthread_mutex_t *round_lock = dp_config_external_round_lock(dp);
	pthread_mutex_lock(round_lock);
	dp_config_assign_worker_ectxs(dp, gen);
	pthread_mutex_unlock(round_lock);
}
*/
import "C"

import "unsafe"

// AssignWorkerContexts delivers a generation's contexts to the workers of
// an in-process dataplane under its round lock, as C's publication does.
//
// The commit handlers run and every worker gets its context.
func AssignWorkerContexts(dp, gen unsafe.Pointer) {
	C.coracle_assign_worker_ectxs((*C.struct_dp_config)(dp), (*C.struct_cp_config_gen)(gen))
}
