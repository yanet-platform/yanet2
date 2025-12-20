use core::error::Error;

use std::net::IpAddr;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    DeleteConfigRequest, ListConfigsRequest, UpdateConfigRequest, forward_service_client::ForwardServiceClient,
};
use commonpb::TargetModule;
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

use serde::{Deserialize, Serialize};

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("forwardpb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// Forward module.
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
    List,
    Delete(DeleteCmd),
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
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
enum ModeKind {
    None,
    In,
    Out,
}

#[derive(Debug, Serialize, Deserialize)]
struct ForwardRule {
    target: String,
    mode: ModeKind,
    counter: String,
    devices: Vec<String>,
    vlan_ranges: Vec<VlanRange>,
    srcs: Vec<Net>,
    dsts: Vec<Net>,
}

impl From<ForwardRule> for code::ForwardRule {
    fn from(forward_rule: ForwardRule) -> Self {
        Self {
            target: forward_rule.target,
            mode: match forward_rule.mode {
                ModeKind::None => code::ForwardMode::None.into(),
                ModeKind::In => code::ForwardMode::In.into(),
                ModeKind::Out => code::ForwardMode::Out.into(),
            },
            counter: forward_rule.counter,
            devices: forward_rule.devices,
            vlan_ranges: forward_rule.vlan_ranges.into_iter().map(|m| m.into()).collect(),
            srcs: forward_rule.srcs.into_iter().map(|m| m.into()).collect(),
            dsts: forward_rule.dsts.into_iter().map(|m| m.into()).collect(),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct ForwardConfig {
    rules: Vec<ForwardRule>,
}

impl From<ForwardConfig> for Vec<code::ForwardRule> {
    fn from(config: ForwardConfig) -> Self {
        config.rules.into_iter().map(From::from).collect()
    }
}

////////////////////////////////////////////////////////////////////////////////

impl ForwardConfig {
    pub fn from_file(path: &str) -> Result<Self, Box<dyn Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

pub struct ForwardService {
    client: ForwardServiceClient<Channel>,
}

impl ForwardService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = ForwardServiceClient::connect(endpoint).await?;
        let client = client
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        println!("{}", serde_json::to_string_pretty(&response.configs)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
        };
        self.client.delete_config(request).await?;

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ForwardConfig::from_file(&cmd.rules)?;
        let rules: Vec<code::ForwardRule> = config.into();
        let request = UpdateConfigRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            rules: rules,
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ForwardService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
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
