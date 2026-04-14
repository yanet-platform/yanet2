package balancer

import (
	"fmt"
	"maps"

	"github.com/c2h5oh/datasize"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"go.uber.org/zap"
)

type Agent struct {
	agent     *yanet.Agent
	balancers map[string]*Balancer
}

func (a *Agent) AsYanetAgent() *yanet.Agent {
	return a.agent
}

func AttachNewAgent(
	shm *yanet.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
) (*Agent, error) {
	agent, err := shm.AgentAttach("balancer", instanceIdx, size)
	if err != nil {
		return nil, fmt.Errorf("failed to attach balancer agent: %w", err)
	}
	return &Agent{
		agent:     agent,
		balancers: make(map[string]*Balancer),
	}, nil
}

func ReattachAgent(
	shm *yanet.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
	log *zap.SugaredLogger,
) (*Agent, error) {
	agent, err := shm.AgentReattach("balancer", instanceIdx, size)
	if err != nil {
		return nil, err
	}

	// Restore balancers
	balancerAgent := &Agent{
		agent:     agent,
		balancers: make(map[string]*Balancer),
	}
	packetHandlers := balancerAgent.list()
	for _, ph := range packetHandlers {
		balancer := restoreBalancerFromPacketHandler(balancerAgent, ph, log)
		name := balancer.handler.name()
		balancerAgent.balancers[name] = balancer
	}

	return balancerAgent, nil
}

// GetBalancer returns the balancer with the given name and whether it exists.
func (a *Agent) GetBalancer(name string) (*Balancer, bool) {
	b, ok := a.balancers[name]
	return b, ok
}

// PutBalancer registers a balancer under the given name.
func (a *Agent) PutBalancer(name string, b *Balancer) {
	a.balancers[name] = b
}

// BalancerNames returns the names of all registered balancers.
func (a *Agent) BalancerNames() []string {
	names := make([]string, 0, len(a.balancers))
	for name := range a.balancers {
		names = append(names, name)
	}
	return names
}

// AllBalancers returns a shallow copy of the balancers map.
func (a *Agent) AllBalancers() map[string]*Balancer {
	result := make(map[string]*Balancer, len(a.balancers))
	maps.Copy(result, a.balancers)
	return result
}
