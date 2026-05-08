package operator

import (
	"maps"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

type RIBStore struct {
	mu   sync.RWMutex
	ribs map[string]*rib.RIB
	log  *zap.Logger
}

func newRIBStore(log *zap.Logger) *RIBStore {
	return &RIBStore{
		ribs: map[string]*rib.RIB{},
		log:  zap.NewNop(),
	}
}

func (m *RIBStore) Configs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0, len(m.ribs))
	for name := range m.ribs {
		out = append(out, name)
	}
	return out
}

func (m *RIBStore) Snapshot() map[string]*rib.RIB {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]*rib.RIB, len(m.ribs))
	maps.Copy(out, m.ribs)
	return out
}

func (m *RIBStore) Get(name string) (*rib.RIB, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ribRef, ok := m.ribs[name]
	return ribRef, ok
}

func (m *RIBStore) GetOrCreate(name string) *rib.RIB {
	m.mu.Lock()
	defer m.mu.Unlock()

	ribRef, ok := m.ribs[name]
	if !ok {
		m.log.Info("created new RIB",
			zap.String("name", name),
		)
		ribRef = rib.NewRIB(m.log)
		m.ribs[name] = ribRef
	}

	return ribRef
}
