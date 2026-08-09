package acl

import (
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
)

var _ fwstate.MapConsumer = (*ACLMapConsumer)(nil)

// ACLMapConsumer adapts [ACLService] to the fwstate MapConsumer contract.
//
// ConfigsUsingMap reports which ACL configs reference a given fwstate-map
// by name. It takes ACLService.mu and does not re-enter FWStateMapService.mu.
type ACLMapConsumer struct {
	service *ACLService
}

// NewACLMapConsumer creates a MapConsumer backed by service.
func NewACLMapConsumer(service *ACLService) *ACLMapConsumer {
	return &ACLMapConsumer{service: service}
}

// ConfigsUsingMap returns ACL config names that reference the given
// fwstate-map name (as either their v4 or v6 fwtable).
//
// Implements fwstate.MapConsumer.ConfigsUsingMap for DeleteMap consumer
// checking.
func (m *ACLMapConsumer) ConfigsUsingMap(mapName string) []string {
	return m.service.ConfigsUsingMap(mapName)
}

// ConfigsUsingMap returns ACL config names whose v4 or v6 fwtable name
// matches mapName.
//
// Caller-facing; takes ACLService.mu internally.
func (m *ACLService) ConfigsUsingMap(mapName string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name, config := range m.configs {
		if config.UsesFwtable(mapName) {
			names = append(names, name)
		}
	}
	return names
}
