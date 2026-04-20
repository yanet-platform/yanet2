package balancer

import (
	"fmt"

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

func (a *Agent) GetBalancer(name string) (*Balancer, bool) {
	b, ok := a.balancers[name]
	return b, ok
}

func (a *Agent) PutBalancer(name string, b *Balancer) {
	a.balancers[name] = b
}

func (a *Agent) BalancerNames() []string {
	names := make([]string, 0, len(a.balancers))
	for name := range a.balancers {
		names = append(names, name)
	}
	return names
}

func (a *Agent) Balancers() map[string]*Balancer {
	return a.balancers
}

func (a *Agent) Close() error {
	for _, balancer := range a.balancers {
		balancer.refresher.Stop()
	}
	return nil
}
