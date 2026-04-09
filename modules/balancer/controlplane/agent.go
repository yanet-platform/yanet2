package balancer

import (
	"fmt"

	"github.com/c2h5oh/datasize"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
)

type BalancerAgent struct {
	agent     *yanet.Agent
	balancers map[string]*Balancer
}

func (a *BalancerAgent) AsYanetAgent() *yanet.Agent {
	return a.agent
}

func AttachNewBalancerAgent(
	shm *yanet.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
) (*BalancerAgent, error) {
	agent, err := shm.AgentAttach("balancer", instanceIdx, size)
	if err != nil {
		return nil, fmt.Errorf("failed to attach balancer agent: %w", err)
	}
	return &BalancerAgent{
		agent:     agent,
		balancers: make(map[string]*Balancer),
	}, nil
}

func ReattachBalancerAgent(
	shm *yanet.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
) (*BalancerAgent, error) {
	agent, err := shm.AgentReattach("balancer", instanceIdx, size)
	if err != nil {
		return nil, fmt.Errorf("failed to reattach balancer agent: %w", err)
	}

	// Restore balancers
	balancerAgent := &BalancerAgent{
		agent:     agent,
		balancers: make(map[string]*Balancer),
	}
	packetHandlers := balancerAgent.list()
	for _, ph := range packetHandlers {
		balancer := restoreBalancerFromPacketHandler(balancerAgent, ph)
		name := balancer.handler.name()
		balancerAgent.balancers[name] = balancer
	}

	return balancerAgent, nil
}

// GetBalancer returns the balancer with the given name and whether it exists.
func (a *BalancerAgent) GetBalancer(name string) (*Balancer, bool) {
	b, ok := a.balancers[name]
	return b, ok
}

// PutBalancer registers a balancer under the given name.
func (a *BalancerAgent) PutBalancer(name string, b *Balancer) {
	a.balancers[name] = b
}

// BalancerNames returns the names of all registered balancers.
func (a *BalancerAgent) BalancerNames() []string {
	names := make([]string, 0, len(a.balancers))
	for name := range a.balancers {
		names = append(names, name)
	}
	return names
}

// AllBalancers returns a shallow copy of the balancers map.
func (a *BalancerAgent) AllBalancers() map[string]*Balancer {
	result := make(map[string]*Balancer, len(a.balancers))
	for k, v := range a.balancers {
		result[k] = v
	}
	return result
}
