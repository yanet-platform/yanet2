use core::error::Error;
use std::{fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    acl_service_client::AclServiceClient,
};
use args::{DeleteCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use netip::IpNetwork;
use serde::{Deserialize, Serialize};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

mod args;

#[allow(non_snake_case)]
pub mod filterpb {
    tonic::include_proto!("filterpb");
}

#[allow(non_snake_case)]
pub mod aclpb {
    tonic::include_proto!("aclpb");
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
struct SerializableShowConfigResponse {
    name: String,
    rules: Vec<SerializableRule>,
}

#[derive(Serialize)]
struct SerializableRule {
    srcs: Vec<String>,
    dsts: Vec<String>,
    src_port_ranges: Vec<String>,
    dst_port_ranges: Vec<String>,
    proto_ranges: Vec<String>,
    vlan_ranges: Vec<String>,
    devices: Vec<String>,
    action: Option<SerializableAction>,
}

#[derive(Serialize)]
struct SerializableAction {
    counter: String,
    keep_state: bool,
    kind: i32,
}

////////////////////////////////////////////////////////////////////////////////

/// ACL module
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

impl TryFrom<&String> for filterpb::IpNet {
    type Error = Box<dyn Error>;

    fn try_from(value: &String) -> Result<Self, Self::Error> {
        let net = IpNetwork::parse(value)?;
        match net {
            IpNetwork::V4(net) => Ok(filterpb::IpNet {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            }),
            IpNetwork::V6(net) => Ok(filterpb::IpNet {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            }),
        }
    }
}

fn format_ip_net(net: &filterpb::IpNet) -> String {
    let prefix_len: u32 = net.mask.iter().map(|b| b.count_ones()).sum();
    if let Ok(arr) = <[u8; 4]>::try_from(net.addr.as_slice()) {
        format!("{}/{}", std::net::Ipv4Addr::from(arr), prefix_len)
    } else if let Ok(arr) = <[u8; 16]>::try_from(net.addr.as_slice()) {
        format!("{}/{}", std::net::Ipv6Addr::from(arr), prefix_len)
    } else {
        format!("{:?}/{}", net.addr, prefix_len)
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Range {
    from: u16,
    to: u16,
}

impl TryFrom<&Range> for filterpb::PortRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
        if r.from > r.to {
            return Err(format!("port 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&Range> for filterpb::ProtoRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
        // Protocol is 16 bits, so valid range is 0-65535
        if r.from > r.to {
            return Err(format!("protocol 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&Range> for filterpb::VlanRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
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
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&String> for filterpb::Device {
    type Error = Box<dyn Error>;

    fn try_from(n: &String) -> Result<Self, Self::Error> {
        Ok(Self { name: n.to_string() })
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ActionKind {
    Allow,
    Deny,
    Count,
    CheckState,
    CreateState,
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
        Ok(Self {
            srcs: acl_rule
                .srcs
                .iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<_, _>>()?,
            dsts: acl_rule
                .dsts
                .iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<_, _>>()?,
            vlan_ranges: acl_rule
                .vlan_ranges
                .iter()
                .map(filterpb::VlanRange::try_from)
                .collect::<Result<_, _>>()?,
            src_port_ranges: acl_rule
                .src_ports
                .iter()
                .map(filterpb::PortRange::try_from)
                .collect::<Result<_, _>>()?,
            dst_port_ranges: acl_rule
                .dst_ports
                .iter()
                .map(filterpb::PortRange::try_from)
                .collect::<Result<_, _>>()?,
            proto_ranges: acl_rule
                .proto_ranges
                .iter()
                .map(filterpb::ProtoRange::try_from)
                .collect::<Result<_, _>>()?,
            devices: acl_rule
                .devices
                .iter()
                .map(filterpb::Device::try_from)
                .collect::<Result<_, _>>()?,
            action: Some(aclpb::Action {
                counter: acl_rule.counter,
                keep_state: false,
                kind: match acl_rule.action {
                    ActionKind::Allow => aclpb::ActionKind::Pass,
                    ActionKind::Deny => aclpb::ActionKind::Deny,
                    ActionKind::Count => aclpb::ActionKind::Count,
                    ActionKind::CheckState => aclpb::ActionKind::CheckState,
                    ActionKind::CreateState => aclpb::ActionKind::CreateState,
                }
                .into(),
            }),
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

impl ACLConfig {
    pub fn from_file<P>(path: P) -> Result<Self, Box<dyn Error>>
    where
        P: AsRef<Path>,
    {
        let rd = File::open(path)?;
        Ok(serde_yaml::from_reader(rd)?)
    }
}

////////////////////////////////////////////////////////////////////////////////

pub struct ACLService {
    client: AclServiceClient<LayeredChannel>,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = AclServiceClient::new(channel)
            .max_decoding_message_size(256 * 1024 * 1024)
            .max_encoding_message_size(256 * 1024 * 1024)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response.configs)?);
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self.client.show_config(request).await?.into_inner();

        let out = SerializableShowConfigResponse {
            name: response.name,
            rules: response
                .rules
                .into_iter()
                .map(|rule| SerializableRule {
                    srcs: rule.srcs.iter().map(format_ip_net).collect(),
                    dsts: rule.dsts.iter().map(format_ip_net).collect(),
                    src_port_ranges: rule
                        .src_port_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    dst_port_ranges: rule
                        .dst_port_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    proto_ranges: rule
                        .proto_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    vlan_ranges: rule
                        .vlan_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    devices: rule.devices.iter().map(|d| d.name.clone()).collect(),
                    action: rule.action.map(|a| SerializableAction {
                        counter: a.counter,
                        keep_state: a.keep_state,
                        kind: a.kind,
                    }),
                })
                .collect(),
        };
        println!("{}", serde_json::to_string(&out)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ACLConfig::from_file(&cmd.rules)?;
        let rules: Vec<aclpb::Rule> = config.try_into()?;
        let request = UpdateConfigRequest { name: cmd.config_name.clone(), rules };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ACLService::new(&cmd.connection).await?;
    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
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
