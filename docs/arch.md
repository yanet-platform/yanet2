# YANET architecture overview

## Configuring the "L3" network function

```mermaid
sequenceDiagram
    autonumber

    participant BA as Balancer Agent
    participant GW as Gateway API
    participant FS as FunctionService
    participant ACL as CP:ACL
    participant BL as CP:Balancer
    participant SM as Shared Memory
    participant DP as Dataplane

    note over BA: desired state: VIP, pools, ACL policy

    par ACL update
      BA->>GW: CP:ACL.Update(ACL rules)
      GW->>ACL: update ACL config
      ACL->>SM: write ACL config
    and Balancer update
      BA->>GW: CP:Balancer.Update(VS/RS)
      GW->>BL: update balancer config
      BL->>SM: write balancer config
    end

    BA->>GW: FunctionService.Update(balancerNF: chains=[ACL->balancer])
    GW->>FS: update balancerNF function
    FS->>SM: apply functions (via temporary function agent)

    DP->>SM: request actual configurations
    SM-->>DP: configurations to apply
    DP->>DP: apply changes

    DP-->>SM: application statuses (modules/functions)
    GW->>SM: request statuses
    SM-->>GW: application statuses
    GW-->>BA: confirmation and status (success/errors)

    loop Monitoring
      DP-->>SM: statuses and counters
      GW->>SM: read statuses/counters
      SM-->>GW: monitoring data
      GW-->>BA: summary state of the network function
    end
```

## System layer pyramid

```text
                                                        +----------------+
                                                        | Cluster        |
                                                   +----+----------------+----+
                                                   | Coordinator              |
                  +------------+--------------+----+--------------------------+----+
                  | CLI        | Web UI       | Network function agents            |
             +----+------------+--------------+------------------[gRPC ]-----------+----+
             | Gateway - single entry point to modules' gRPC APIs, proxy                |
        +----+---------------------------------------------------[gRPC ]----------------+----+
        | CP - configuration, shmem fill, gRPC API                                           |
   +----+--------------------------------------------------------[SHMEM]---------------------+----+
   | Dataplane — modules, network functions, pipelines                                            |
   +----------------------------------------------------------------------------------------------+
```

**Invariants**:

- Lower layers continue to work if the upper ones fail (DP processes traffic with the last valid configuration).
- Upper layers only configure and augment the lower ones, but do not replace them.
