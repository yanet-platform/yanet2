use core::{error::Error, net::IpAddr};
use std::{fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest, UpdateFwStateConfigRequest,
    acl_service_client::AclServiceClient,
};
use args::{DeleteCmd, ModeCmd, SetFwstateConfigCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::TargetModule;
use ipnetwork::IpNetwork;
use serde::{Deserialize, Serialize};
use tonic::transport::Channel;
use ync::logging;

mod args;
mod format;

#[allow(non_snake_case)]
pub mod aclpb {
    use serde::Serialize;

    tonic::include_proto!("aclpb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// ACL module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl TryFrom<VlanRange> for aclpb::VlanRange {
    type Error = Box<dyn Error>;

    fn try_from(r: VlanRange) -> Result<Self, Self::Error> {
        // VLAN ID is 12 bits, so valid range is 0-4095
        if r.from > 4095 {
            return Err(format!("VLAN 'from' value {} exceeds maximum 4095", r.from).into());
        }
        if r.to > 4095 {
            return Err(format!("VLAN 'to' value {} exceeds maximum 4095", r.to).into());
        }
        if r.from > r.to {
            return Err(format!("VLAN 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from, to: r.to })
    }
}

impl TryFrom<String> for aclpb::IpNet {
    type Error = Box<dyn Error>;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        let parts: Vec<&str> = value.split('/').collect();
        if parts.len() == 1 {
            let addr: IpAddr = value.parse()?;
            return Ok(match addr {
                IpAddr::V4(v4) => aclpb::IpNet {
                    addr: v4.octets().to_vec(),
                    mask: [0xff, 0xff, 0xff, 0xff].to_vec(),
                },
                IpAddr::V6(v6) => aclpb::IpNet {
                    addr: v6.octets().to_vec(),
                    mask: [
                        0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
                    ]
                    .to_vec(),
                },
            });
        }

        if parts.len() != 2 {
            return Err("invalid format: expected IP/mask or IP/prefix".into());
        }

        let network_result: Result<IpNetwork, _> = value.parse();

        match network_result {
            Ok(net) => Ok(aclpb::IpNet {
                addr: match net.ip() {
                    IpAddr::V4(v4) => v4.octets().to_vec(),
                    IpAddr::V6(v6) => v6.octets().to_vec(),
                },
                mask: match net.mask() {
                    IpAddr::V4(v4) => v4.octets().to_vec(),
                    IpAddr::V6(v6) => v6.octets().to_vec(),
                },
            }),
            Err(_) => {
                let addr: IpAddr = parts[0].parse()?;
                let mask: IpAddr = parts[1].parse()?;
                Ok(aclpb::IpNet {
                    addr: match addr {
                        IpAddr::V4(v4) => v4.octets().to_vec(),
                        IpAddr::V6(v6) => v6.octets().to_vec(),
                    },
                    mask: match mask {
                        IpAddr::V4(v4) => v4.octets().to_vec(),
                        IpAddr::V6(v6) => v6.octets().to_vec(),
                    },
                })
            }
        }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Range {
    from: u32,
    to: u32,
}

impl TryFrom<Range> for aclpb::PortRange {
    type Error = Box<dyn Error>;

    fn try_from(r: Range) -> Result<Self, Self::Error> {
        if r.from > 65535 {
            return Err(format!("Port 'from' value {} exceeds maximum 65535", r.from).into());
        }
        if r.to > 65535 {
            return Err(format!("Port 'to' value {} exceeds maximum 65535", r.to).into());
        }
        if r.from > r.to {
            return Err(format!("Port 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from, to: r.to })
    }
}

impl TryFrom<Range> for aclpb::ProtoRange {
    type Error = Box<dyn Error>;

    fn try_from(r: Range) -> Result<Self, Self::Error> {
        if r.from > 65535 {
            return Err(format!("Protocol 'from' value {} exceeds maximum 65535", r.from).into());
        }
        if r.to > 65535 {
            return Err(format!("Protocol 'to' value {} exceeds maximum 65535", r.to).into());
        }
        if r.from > r.to {
            return Err(format!("Protocol 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from, to: r.to })
    }
}

impl TryFrom<Range> for aclpb::VlanRange {
    type Error = Box<dyn Error>;

    fn try_from(r: Range) -> Result<Self, Self::Error> {
        // VLAN ID is 12 bits, so valid range is 0-4095
        if r.from > 4095 {
            return Err(format!("VLAN 'from' value {} exceeds maximum 4095", r.from).into());
        }
        if r.to > 4095 {
            return Err(format!("VLAN 'to' value {} exceeds maximum 4095", r.to).into());
        }
        if r.from > r.to {
            return Err(format!("VLAN 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from, to: r.to })
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ActionKind {
    Allow,
    Deny,
}

#[derive(Debug, Serialize, Deserialize)]
struct ACLRule {
    srcs: Vec<String>,
    dsts: Vec<String>,
    src_ports: Vec<Range>,
    dst_ports: Vec<Range>,
    proto_ranges: Vec<Range>,
    vlan_ranges: Vec<Range>,
    devices: Vec<String>,
    counter: String,
    action: ActionKind,
}

impl TryFrom<ACLRule> for aclpb::Rule {
    type Error = Box<dyn Error>;

    fn try_from(acl_rule: ACLRule) -> Result<Self, Self::Error> {
        let srcs: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .srcs
            .into_iter()
            .enumerate()
            .map(|(i, m)| {
                m.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse src[{}]: {}", i, e).into()
                })
            })
            .collect();

        let dsts: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .dsts
            .into_iter()
            .enumerate()
            .map(|(i, m)| {
                m.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse dst[{}]: {}", i, e).into()
                })
            })
            .collect();

        let vlan_ranges: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .vlan_ranges
            .into_iter()
            .enumerate()
            .map(|(i, r)| {
                r.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse vlan_range[{}]: {}", i, e).into()
                })
            })
            .collect();

        let src_port_ranges: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .src_ports
            .into_iter()
            .enumerate()
            .map(|(i, r)| {
                r.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse src_port_range[{}]: {}", i, e).into()
                })
            })
            .collect();

        let dst_port_ranges: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .dst_ports
            .into_iter()
            .enumerate()
            .map(|(i, r)| {
                r.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse dst_port_range[{}]: {}", i, e).into()
                })
            })
            .collect();

        let proto_ranges: Result<Vec<_>, Box<dyn Error>> = acl_rule
            .proto_ranges
            .into_iter()
            .enumerate()
            .map(|(i, r)| {
                r.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse proto_range[{}]: {}", i, e).into()
                })
            })
            .collect();

        Ok(Self {
            counter: acl_rule.counter,
            devices: acl_rule.devices,
            vlan_ranges: vlan_ranges?,
            srcs: srcs?,
            dsts: dsts?,
            src_port_ranges: src_port_ranges?,
            dst_port_ranges: dst_port_ranges?,
            proto_ranges: proto_ranges?,
            keep_state: false,
            action: match acl_rule.action {
                ActionKind::Allow => aclpb::ActionKind::Pass,
                ActionKind::Deny => aclpb::ActionKind::Deny,
            }
            .into(),
        })
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct ACLConfig {
    rules: Vec<ACLRule>,
}

impl TryFrom<ACLConfig> for Vec<aclpb::Rule> {
    type Error = Box<dyn Error>;

    fn try_from(config: ACLConfig) -> Result<Self, Self::Error> {
        config
            .rules
            .into_iter()
            .enumerate()
            .map(|(i, rule)| {
                rule.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse rule #{}: {}", i + 1, e).into()
                })
            })
            .collect()
    }
}

////////////////////////////////////////////////////////////////////////////////

impl ACLConfig {
    pub fn from_file<P>(path: P) -> Result<Self, Box<dyn Error>>
    where
        P: AsRef<Path>,
    {
        let rd = File::open(path)?;
        let cfg = serde_yaml::from_reader(rd)?;

        Ok(cfg)
    }
}

pub struct ACLService {
    client: AclServiceClient<Channel>,
}

impl ACLService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = AclServiceClient::connect(endpoint).await?;
        let client = client.max_decoding_message_size(256 * 1024 * 1024);
        let client = client.max_encoding_message_size(256 * 1024 * 1024);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response.configs)?);
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
        };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ACLConfig::from_file(&cmd.rules)?;
        let rules: Vec<aclpb::Rule> = config.try_into()?;
        let request = UpdateConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            rules,
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");
        Ok(())
    }

    pub async fn set_fwstate_config(&mut self, cmd: SetFwstateConfigCmd) -> Result<(), Box<dyn Error>> {
        // First, fetch the current config to merge with new values
        let current_request = ShowConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
        };
        let current_response = self.client.show_config(current_request).await?.into_inner();

        // Start with existing config or create a new one
        let mut map_config = current_response.fwstate_map.unwrap_or_default();
        let mut sync_config = current_response.fwstate_sync.unwrap_or_default();

        // Update map config fields if provided
        if let Some(index_size) = cmd.index_size {
            map_config.index_size = index_size;
        }

        if let Some(extra_bucket_count) = cmd.extra_bucket_count {
            map_config.extra_bucket_count = extra_bucket_count;
        }

        // Update only the fields that were provided
        if let Some(ref src_addr) = cmd.src_addr {
            sync_config.src_addr = format::parse_ipv6(src_addr)?;
        }

        if let Some(ref dst_ether) = cmd.dst_ether {
            sync_config.dst_ether = format::parse_mac(dst_ether)?;
        }

        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast = format::parse_ipv6(dst_addr_multicast)?;
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = port_multicast;
        }

        if let Some(ref dst_addr_unicast) = cmd.dst_addr_unicast {
            sync_config.dst_addr_unicast = format::parse_ipv6(dst_addr_unicast)?;
        }

        if let Some(port_unicast) = cmd.port_unicast {
            sync_config.port_unicast = port_unicast;
        }

        // Convert timeouts from Duration to nanoseconds if provided
        if let Some(tcp_syn_ack) = cmd.tcp_syn_ack {
            sync_config.tcp_syn_ack = tcp_syn_ack.as_nanos() as u64;
        }

        if let Some(tcp_syn) = cmd.tcp_syn {
            sync_config.tcp_syn = tcp_syn.as_nanos() as u64;
        }

        if let Some(tcp_fin) = cmd.tcp_fin {
            sync_config.tcp_fin = tcp_fin.as_nanos() as u64;
        }

        if let Some(tcp) = cmd.tcp {
            sync_config.tcp = tcp.as_nanos() as u64;
        }

        if let Some(udp) = cmd.udp {
            sync_config.udp = udp.as_nanos() as u64;
        }

        if let Some(default) = cmd.default {
            sync_config.default = default.as_nanos() as u64;
        }

        let request = UpdateFwStateConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            map_config: Some(map_config),
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateFWStateConfigRequest: {request:?}");
        let response = self.client.update_fw_state_config(request).await?.into_inner();
        log::debug!("UpdateFWStateConfigResponse: {response:?}");
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ACLService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::SetFwstateConfig(cmd) => service.set_fwstate_config(cmd).await,
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
