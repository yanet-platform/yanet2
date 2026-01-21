# Balancer CLI Refactoring Implementation Guide

This document provides a complete guide for refactoring the balancer CLI to work with the new protobuf interface.

## Overview

The refactoring updates the CLI to work with:
- New protobuf location: `modules/balancer/agent/balancerpb/`
- Hierarchical topology-aware structures
- New identifier types (VsIdentifier, RealIdentifier)
- Renamed RPC methods
- Simplified YAML configuration format
- Flexible parsing for schedulers and protocols

## Files to Modify

### 1. build.rs ✅ COMPLETED
- Updated proto file paths from `../controlplane/balancerpb/` to `../agent/balancerpb/`
- Added `graph.proto` to compilation

### 2. entities.rs (MAJOR CHANGES NEEDED)

#### New Structures:
```rust
// Top-level config with optional fields for partial updates
pub struct BalancerConfig {
    pub packet_handler: Option<PacketHandlerConfig>,
    pub state: Option<StateConfig>,
}

// Flat YAML structure for VS (no nested 'id')
pub struct VirtualService {
    pub addr: String,
    pub port: u32,
    pub proto: Proto,
    pub scheduler: Scheduler,
    pub flags: VsFlags,
    pub allowed_srcs: Vec<String>,  // CIDR format
    pub reals: Vec<Real>,
    pub peers: Vec<String>,
}

// Flat YAML structure for Real (no nested 'id')
pub struct Real {
    pub ip: String,
    pub port: u32,
    pub weight: u32,
    pub src_addr: String,
    pub src_mask: String,
}

// State config with grouped session_table
pub struct StateConfig {
    pub session_table: Option<SessionTableConfig>,
    pub wlc: Option<WlcConfig>,
    pub refresh_period_ms: Option<u64>,
}

pub struct SessionTableConfig {
    pub capacity: u64,
    pub max_load_factor: f32,
}
```

#### Flexible Parsing:
```rust
// Scheduler: SOURCE_HASH, source_hash, SH, sh, ROUND_ROBIN, round_robin, RR, rr
impl<'de> Deserialize<'de> for Scheduler {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error> {
        let s = String::deserialize(deserializer)?;
        match s.to_uppercase().as_str() {
            "SOURCE_HASH" | "SH" => Ok(Scheduler::SourceHash),
            "ROUND_ROBIN" | "RR" => Ok(Scheduler::RoundRobin),
            _ => Err(...)
        }
    }
}

// Proto: TCP, tcp, UDP, udp
impl<'de> Deserialize<'de> for Proto {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error> {
        let s = String::deserialize(deserializer)?;
        match s.to_uppercase().as_str() {
            "TCP" => Ok(Proto::Tcp),
            "UDP" => Ok(Proto::Udp),
            _ => Err(...)
        }
    }
}
```

#### CIDR Parsing:
```rust
pub fn parse_cidr(cidr: &str) -> Result<(IpAddr, u32), String> {
    let parts: Vec<&str> = cidr.split('/').collect();
    if parts.len() != 2 {
        return Err(format!("invalid CIDR format: '{}'", cidr));
    }
    let addr: IpAddr = parts[0].parse()?;
    let size: u32 = parts[1].parse()?;
    // Validate size (0-32 for IPv4, 0-128 for IPv6)
    Ok((addr, size))
}
```

#### Conversion to Protobuf:
```rust
impl TryFrom<VirtualService> for balancerpb::VirtualService {
    fn try_from(vs: VirtualService) -> Result<Self> {
        // Create VsIdentifier from flat fields
        let id = Some(balancerpb::VsIdentifier {
            addr: Some(balancerpb::Addr {
                bytes: ip_to_bytes(vs.addr.parse()?),
            }),
            port: vs.port,
            proto: match vs.proto {
                Proto::Tcp => balancerpb::TransportProto::Tcp,
                Proto::Udp => balancerpb::TransportProto::Udp,
            } as i32,
        });
        
        // Parse CIDR for allowed_srcs
        let allowed_srcs = vs.allowed_srcs.iter()
            .map(|cidr| {
                let (addr, size) = parse_cidr(cidr)?;
                Ok(balancerpb::Net {
                    addr: Some(balancerpb::Addr { bytes: ip_to_bytes(addr) }),
                    size,
                })
            })
            .collect::<Result<Vec<_>>>()?;
        
        Ok(Self { id, allowed_srcs, ... })
    }
}
```

### 3. cmd.rs (MODERATE CHANGES)

#### Add Graph Command:
```rust
pub enum Mode {
    Update(UpdateCmd),
    Reals(RealsCmd),
    Config(ConfigCmd),
    List(ListCmd),
    Stats(StatsCmd),
    Info(InfoCmd),      // Renamed from State
    Sessions(SessionsCmd),
    Graph(GraphCmd),    // NEW
}

#[derive(Debug, Clone, Parser)]
pub struct GraphCmd {
    #[arg(long, short = 'n')]
    pub name: String,
    
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}
```

#### Update Stats Command:
```rust
#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    #[arg(long, short = 'n')]
    pub name: String,
    
    // Optional filters
    #[arg(long)]
    pub device: Option<String>,
    
    #[arg(long)]
    pub pipeline: Option<String>,
    
    #[arg(long)]
    pub function: Option<String>,
    
    #[arg(long)]
    pub chain: Option<String>,
    
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&StatsCmd> for balancerpb::ShowStatsRequest {
    fn from(cmd: &StatsCmd) -> Self {
        Self {
            name: cmd.name.clone(),
            ref_: Some(balancerpb::PacketHandlerRef {
                device: cmd.device.clone(),
                pipeline: cmd.pipeline.clone(),
                function: cmd.function.clone(),
                chain: cmd.chain.clone(),
            }),
        }
    }
}
```

#### Update Real Commands:
```rust
impl TryFrom<EnableRealCmd> for balancerpb::UpdateRealsRequest {
    fn try_from(cmd: EnableRealCmd) -> Result<Self> {
        let proto = match cmd.proto.to_uppercase().as_str() {
            "TCP" => balancerpb::TransportProto::Tcp,
            "UDP" => balancerpb::TransportProto::Udp,
            _ => return Err(...),
        };
        
        let real_id = balancerpb::RealIdentifier {
            vs: Some(balancerpb::VsIdentifier {
                addr: Some(balancerpb::Addr {
                    bytes: cmd.virtual_ip.parse()?.octets().to_vec(),
                }),
                port: cmd.virtual_port as u32,
                proto: proto as i32,
            }),
            real: Some(balancerpb::RelativeRealIdentifier {
                ip: Some(balancerpb::Addr {
                    bytes: cmd.real_ip.parse()?.octets().to_vec(),
                }),
                port: 0,
            }),
        };
        
        Ok(Self {
            name: cmd.name,
            updates: vec![balancerpb::RealUpdate {
                real_id: Some(real_id),
                enable: Some(true),
                weight: cmd.weight,
            }],
            buffer: true,
        })
    }
}
```

### 4. service.rs (METHOD RENAMES)

```rust
impl BalancerService {
    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<()> {
        match mode {
            Mode::Update(cmd) => self.update_config(cmd).await,
            Mode::Reals(cmd) => self.handle_reals(cmd).await,
            Mode::Config(cmd) => self.config(cmd).await,
            Mode::List(cmd) => self.list(cmd).await,
            Mode::Stats(cmd) => self.stats(cmd).await,
            Mode::Info(cmd) => self.info(cmd).await,        // Renamed
            Mode::Sessions(cmd) => self.sessions(cmd).await,
            Mode::Graph(cmd) => self.graph(cmd).await,      // NEW
        }
    }
    
    // Renamed methods
    async fn stats(&mut self, cmd: StatsCmd) -> Result<()> {
        let request = (&cmd).into();
        let response = self.client.show_stats(request).await?.into_inner();
        output::print_show_stats(&response, cmd.format.into())?;
        Ok(())
    }
    
    async fn info(&mut self, cmd: InfoCmd) -> Result<()> {
        let request = (&cmd).into();
        let response = self.client.show_info(request).await?.into_inner();
        output::print_show_info(&response, cmd.format.into())?;
        Ok(())
    }
    
    async fn sessions(&mut self, cmd: SessionsCmd) -> Result<()> {
        let request = (&cmd).into();
        let response = self.client.show_sessions(request).await?.into_inner();
        output::print_show_sessions(&response, cmd.format.into())?;
        Ok(())
    }
    
    // NEW method
    async fn graph(&mut self, cmd: GraphCmd) -> Result<()> {
        let request = balancerpb::ShowGraphRequest {
            name: cmd.name.clone(),
        };
        let response = self.client.show_graph(request).await?.into_inner();
        output::print_show_graph(&response, cmd.format.into())?;
        Ok(())
    }
}
```

### 5. output.rs (MAJOR REFACTORING)

#### Helper Functions:
```rust
// Update to work with Addr messages
fn addr_to_string(addr: Option<&balancerpb::Addr>) -> String {
    addr.and_then(|a| bytes_to_ip(&a.bytes).ok())
        .map(|ip| ip.to_string())
        .unwrap_or_default()
}

fn proto_to_string(proto: i32) -> String {
    match balancerpb::TransportProto::try_from(proto) {
        Ok(balancerpb::TransportProto::Tcp) => "TCP",
        Ok(balancerpb::TransportProto::Udp) => "UDP",
        _ => "Unknown",
    }.to_string()
}

fn scheduler_to_string(sched: i32) -> String {
    match balancerpb::VsScheduler::try_from(sched) {
        Ok(balancerpb::VsScheduler::SourceHash) => "source_hash",
        Ok(balancerpb::VsScheduler::RoundRobin) => "round_robin",
        _ => "Unknown",
    }.to_string()
}
```

#### ShowConfig Output:
```rust
pub fn print_show_config(
    response: &balancerpb::ShowConfigResponse,
    format: OutputFormat,
) -> Result<()> {
    // Access via response.config.packet_handler and response.config.state
    let config = response.config.as_ref().ok_or("missing config")?;
    
    if let Some(ph) = &config.packet_handler {
        for vs in &ph.vs {
            let id = vs.id.as_ref().ok_or("missing VS id")?;
            let addr = addr_to_string(id.addr.as_ref());
            // Display VS with reals nested under it
            for real in &vs.reals {
                let real_id = real.id.as_ref().ok_or("missing real id")?;
                let real_ip = addr_to_string(real_id.ip.as_ref());
                // ...
            }
        }
    }
    
    if let Some(state) = &config.state {
        // Display session_table_capacity, session_table_max_load_factor
        // Display wlc config
        // Display refresh_period
    }
}
```

#### ShowInfo Output (Hierarchical):
```rust
pub fn print_show_info(
    response: &balancerpb::ShowInfoResponse,
    format: OutputFormat,
) -> Result<()> {
    let info = response.info.as_ref().ok_or("missing info")?;
    
    // Display hierarchically: VS contains Reals
    for vs_info in &info.vs {
        let id = vs_info.id.as_ref().ok_or("missing VS id")?;
        let addr = addr_to_string(id.addr.as_ref());
        
        println!("VS {}:{}/{}", addr, id.port, proto_to_string(id.proto));
        println!("  Active Sessions: {}", vs_info.active_sessions);
        
        // Display reals under this VS
        for real_info in &vs_info.reals {
            let real_id = real_info.id.as_ref().ok_or("missing real id")?;
            let real_ip = addr_to_string(real_id.real.as_ref()?.ip.as_ref());
            println!("    Real {}: {} sessions", real_ip, real_info.active_sessions);
        }
    }
}
```

#### ShowStats Output (Hierarchical):
```rust
pub fn print_show_stats(
    response: &balancerpb::ShowStatsResponse,
    format: OutputFormat,
) -> Result<()> {
    let stats = response.stats.as_ref().ok_or("missing stats")?;
    
    // Display hierarchically: NamedVsStats contains NamedRealStats
    for vs_stats in &stats.vs {
        let vs_id = vs_stats.vs.as_ref().ok_or("missing VS id")?;
        let addr = addr_to_string(vs_id.addr.as_ref());
        
        println!("VS {}:{}/{}", addr, vs_id.port, proto_to_string(vs_id.proto));
        if let Some(s) = &vs_stats.stats {
            println!("  Packets: {}", s.incoming_packets);
        }
        
        // Display real stats under this VS
        for real_stats in &vs_stats.reals {
            let real_id = real_stats.real.as_ref().ok_or("missing real id")?;
            let real_ip = addr_to_string(real_id.real.as_ref()?.ip.as_ref());
            println!("    Real {}", real_ip);
            if let Some(s) = &real_stats.stats {
                println!("      Packets: {}", s.packets);
            }
        }
    }
}
```

#### ShowSessions Output:
```rust
pub fn print_show_sessions(
    response: &balancerpb::ShowSessionsResponse,
    format: OutputFormat,
) -> Result<()> {
    for session in &response.sessions {
        let client_addr = addr_to_string(session.client_addr.as_ref());
        let vs_id = session.vs_id.as_ref().ok_or("missing VS id")?;
        let vs_addr = addr_to_string(vs_id.addr.as_ref());
        let real_id = session.real_id.as_ref().ok_or("missing real id")?;
        let real_addr = addr_to_string(real_id.real.as_ref()?.ip.as_ref());
        
        println!("{}:{} -> {}:{} -> {}",
            client_addr, session.client_port,
            vs_addr, vs_id.port,
            real_addr);
    }
}
```

#### ShowGraph Output (NEW):
```rust
pub fn print_show_graph(
    response: &balancerpb::ShowGraphResponse,
    format: OutputFormat,
) -> Result<()> {
    let graph = response.graph.as_ref().ok_or("missing graph")?;
    
    for vs in &graph.virtual_services {
        let id = vs.identifier.as_ref().ok_or("missing VS id")?;
        let addr = addr_to_string(id.addr.as_ref());
        
        println!("VS {}:{}/{}", addr, id.port, proto_to_string(id.proto));
        
        for real in &vs.reals {
            let real_id = real.identifier.as_ref().ok_or("missing real id")?;
            let real_ip = addr_to_string(real_id.ip.as_ref());
            println!("  Real {}: weight={}, effective_weight={}, enabled={}",
                real_ip, real.weight, real.effective_weight, real.enabled);
        }
    }
}
```

### 6. json_output.rs (UPDATE ALL CONVERSIONS)

Update all conversion functions to work with new structures:
- Use `Addr` messages instead of raw bytes
- Handle hierarchical structures (VS contains Reals)
- Add `convert_show_graph()` function

## Testing Checklist

- [ ] Build succeeds with new proto files
- [ ] Update command works with full config
- [ ] Update command works with packet_handler only
- [ ] Update command works with state only
- [ ] Flexible scheduler parsing (SOURCE_HASH, SH, etc.)
- [ ] Flexible protocol parsing (TCP, tcp, etc.)
- [ ] CIDR parsing for allowed_srcs
- [ ] Empty allowed_srcs = allow none
- [ ] Real enable/disable with new identifiers
- [ ] ShowConfig displays hierarchy correctly
- [ ] ShowInfo displays reals under VS
- [ ] ShowStats displays reals under VS
- [ ] ShowSessions uses new identifiers
- [ ] ShowGraph displays topology
- [ ] All output formats work (JSON, Tree, Table)

## Example YAML Configs

See `example-config.yaml` for full configuration example with all features demonstrated.