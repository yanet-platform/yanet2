package nat64s_test

/*
#cgo CFLAGS: -I../../../../

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

// yanet_test_config_lock takes the agent's configuration lock and returns
// once the calling thread holds it.
static void
yanet_test_config_lock(struct agent *agent) {
	cp_config_lock(ADDR_OF(&agent->cp_config));
}

// yanet_test_config_unlock releases the configuration lock the calling
// thread holds.
static void
yanet_test_config_unlock(struct agent *agent) {
	cp_config_unlock(ADDR_OF(&agent->cp_config));
}
*/
import "C"

import (
	"runtime"
	"testing"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// withConfigLock runs locked with the agent's configuration lock held on
// its own thread, so an agent operation that needs the lock, a map's
// deletion among them, only proceeds once locked has returned.
//
// The lock belongs to the thread that took it, hence the thread is pinned
// for the duration.
func withConfigLock(t *testing.T, agent *ffi.Agent, locked func()) {
	t.Helper()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	raw := (*C.struct_agent)(agent.AsRawPtr())
	C.yanet_test_config_lock(raw)
	defer C.yanet_test_config_unlock(raw)

	locked()
}
