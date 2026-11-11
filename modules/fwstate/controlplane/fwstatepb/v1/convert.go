package fwstatepb

import "google.golang.org/protobuf/proto"

// Merge overwrites the settings the update carries and keeps the rest.
//
// A carried update replaces both lists whole - an empty list clears,
// proto3 drawing no difference between an absent and an empty repeated
// field - and proto.Merge alone would append to them, so each list is
// rebuilt around it. Replacement entries are cloned, keeping
// proto.Merge's deep-copy contract for the caller's messages.
func (m *SyncConfig) Merge(update *SyncConfig) {
	if update == nil {
		return
	}

	proto.Merge(m, update)
	endpoints := make([]*SyncEndpoint, len(update.GetEndpoints()))
	for idx, endpoint := range update.GetEndpoints() {
		endpoints[idx] = proto.CloneOf(endpoint)
	}
	m.Endpoints = endpoints
	listeners := make([]*SyncListenEndpoint, len(update.GetListeners()))
	for idx, listener := range update.GetListeners() {
		listeners[idx] = proto.CloneOf(listener)
	}
	m.Listeners = listeners
}
