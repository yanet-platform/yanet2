package main

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

func main() {
	shm, err := ffi.AttachSharedMemory("/dev/hugepages/yanet")
	if err != nil {
		fmt.Printf("failed to attach shared memory: %s", err)
		os.Exit(1)
	}
	agent, err := shm.AgentAttach("balancer", 0, 1<<25)
	if err != nil {
		fmt.Printf("failed to attach agent: %s", err)
		os.Exit(1)
	}

	println("works good!")
	fmt.Printf("&agent=%d", agent.AsRawPtr())

	// balancer := balancer.NewBalancerInstance(agent)

	os.Exit(0)
}
