package fwstatepb

import "google.golang.org/protobuf/proto"

// Merge overwrites the settings the update carries and keeps the rest.
//
// An update carrying endpoints or listeners replaces that whole list, and
// no_endpoints or no_listeners removes it; proto.Merge alone would append
// to the repeated fields, so each list is restored around it. Replacement
// entries are cloned, keeping proto.Merge's deep-copy contract for the
// caller's messages.
func (m *SyncConfig) Merge(update *SyncConfig) {
	if update == nil {
		return
	}

	storedEndpoints := m.GetEndpoints()
	storedListeners := m.GetListeners()
	proto.Merge(m, update)
	switch {
	case update.GetNoEndpoints():
		m.Endpoints = nil
	case len(update.GetEndpoints()) > 0:
		endpoints := make([]*SyncEndpoint, len(update.GetEndpoints()))
		for idx, endpoint := range update.GetEndpoints() {
			endpoints[idx] = proto.CloneOf(endpoint)
		}
		m.Endpoints = endpoints
	default:
		m.Endpoints = storedEndpoints
	}
	switch {
	case update.GetNoListeners():
		m.Listeners = nil
	case len(update.GetListeners()) > 0:
		listeners := make([]*SyncListenEndpoint, len(update.GetListeners()))
		for idx, listener := range update.GetListeners() {
			listeners[idx] = proto.CloneOf(listener)
		}
		m.Listeners = listeners
	default:
		m.Listeners = storedListeners
	}
}
