package acl

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// aclMetricsSnapshot is read-only after publication.
type aclMetricsSnapshot struct {
	configInfos map[string]cacl.AclConfigInfo
}

func (m *aclMetricsSnapshot) containsConfig(name string) bool {
	_, ok := m.configInfos[name]
	return ok
}

func (m *aclMetricsSnapshot) configInfo(name string) (cacl.AclConfigInfo, bool) {
	info, ok := m.configInfos[name]
	return info, ok
}

// aclMetricsState owns the atomically published metrics metadata.
type aclMetricsState struct {
	snapshot atomic.Pointer[aclMetricsSnapshot]
}

func newACLMetricsState() *aclMetricsState {
	m := &aclMetricsState{}
	m.publish(make(map[string]cacl.AclConfigInfo))
	return m
}

func (m *aclMetricsState) load() *aclMetricsSnapshot {
	return m.snapshot.Load()
}

func (m *aclMetricsState) publish(configInfos map[string]cacl.AclConfigInfo) {
	m.snapshot.Store(&aclMetricsSnapshot{configInfos: configInfos})
}
