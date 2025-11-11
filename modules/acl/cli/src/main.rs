use core::error::Error;

use std::net::IpAddr;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{DeleteConfigRequest, UpdateConfigRequest, acl_service_client::AclServiceClient};
use commonpb::TargetModule;
use tonic::transport::Channel;
use ync::logging;

use serde::{Deserialize, Serialize};

#[allow(non_snake_case)]
pub mod code {
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

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Delete(DeleteCmd),
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances from which to delete config
    #[arg(long, short, required = true)]
    pub instance: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances where the changes should be applied.
    #[arg(long, short, required = true)]
    pub instance: u32,
    /// Ruleset file name.
    #[arg(required = true, long = "rules", value_name = "rules")]
    pub rules: String,
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl From<VlanRange> for code::VlanRange {
    fn from(r: VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Net {
    addr: String,
    prefix: u32,
}

impl From<Net> for code::IpNet {
    fn from(value: Net) -> Self {
        let net: IpAddr = value.addr.parse().unwrap();
        Self {
            ip: match net {
                IpAddr::V4(ipv4) => ipv4.octets().into(),
                IpAddr::V6(ipv6) => ipv6.octets().into(),
            },
            prefix_len: value.prefix,
        }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Range {
    from: u32,
    to: u32,
}

impl From<Range> for code::PortRange {
    fn from(r: Range) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl From<Range> for code::ProtoRange {
    fn from(r: Range) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl From<Range> for code::VlanRange {
    fn from(r: Range) -> Self {
        Self { from: r.from, to: r.to }
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ActionKind {
    Allow,
    Deny,
}

#[derive(Debug, Serialize, Deserialize)]
struct ACLRule {
    srcs: Vec<Net>,
    dsts: Vec<Net>,
    src_ports: Vec<Range>,
    dst_ports: Vec<Range>,
    proto_ranges: Vec<Range>,
    vlan_ranges: Vec<Range>,
    devices: Vec<String>,
    counter: String,
    action: ActionKind,
}

impl From<ACLRule> for code::Rule {
    fn from(acl_rule: ACLRule) -> Self {
        Self {
            counter: acl_rule.counter,
            devices: acl_rule.devices,
            vlan_ranges: acl_rule.vlan_ranges.into_iter().map(|m| m.into()).collect(),
            srcs: acl_rule.srcs.into_iter().map(|m| m.into()).collect(),
            dsts: acl_rule.dsts.into_iter().map(|m| m.into()).collect(),
            src_port_ranges: acl_rule.src_ports.into_iter().map(|m| m.into()).collect(),
            dst_port_ranges: acl_rule.dst_ports.into_iter().map(|m| m.into()).collect(),
            proto_ranges: acl_rule.proto_ranges.into_iter().map(|m| m.into()).collect(),
            keep_state: false,
            action: match acl_rule.action {
                ActionKind::Allow => code::ActionKind::Pass,
                ActionKind::Deny => code::ActionKind::Deny,
            }
            .into(),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct ACLConfig {
    rules: Vec<ACLRule>,
}

impl From<ACLConfig> for Vec<code::Rule> {
    fn from(config: ACLConfig) -> Self {
        config.rules.into_iter().map(From::from).collect()
    }
}

////////////////////////////////////////////////////////////////////////////////

impl ACLConfig {
    pub fn from_file(path: &str) -> Result<Self, Box<dyn Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

pub struct ACLService {
    client: AclServiceClient<Channel>,
}

impl ACLService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = AclServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest {
            target: Some(TargetModule {
                config_name: cmd.config_name.clone(),
                dataplane_instance: cmd.instance,
            }),
        };
        self.client.delete_config(request).await?;

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ACLConfig::from_file(&cmd.rules)?;
        let rules: Vec<code::Rule> = config.into();
        let request = UpdateConfigRequest {
            target: Some(TargetModule {
                config_name: cmd.config_name.clone(),
                dataplane_instance: cmd.instance,
            }),
            rules: rules,
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ACLService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
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
