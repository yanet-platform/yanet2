package fwstatepb

import "google.golang.org/protobuf/proto"

// Merge overwrites the settings the update carries and keeps the rest.
//
// An update carrying endpoints replaces the whole list and no_endpoints
// removes it; proto.Merge alone would append to the repeated field, so the
// list is restored around it.
func (m *SyncConfig) Merge(update *SyncConfig) {
	if update == nil {
		return
	}

	stored := m.GetEndpoints()
	proto.Merge(m, update)
	switch {
	case update.GetNoEndpoints():
		m.Endpoints = nil
	case len(update.GetEndpoints()) > 0:
		m.Endpoints = update.GetEndpoints()
	default:
		m.Endpoints = stored
	}
}
