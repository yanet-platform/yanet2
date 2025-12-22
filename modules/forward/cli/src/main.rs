use core::error::Error;

use std::net::IpAddr;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use forwardpb::{
    DeleteConfigRequest, ListConfigsRequest, UpdateConfigRequest,
    ShowConfigRequest, ShowConfigResponse, forward_service_client::ForwardServiceClient,
};
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;
use ipnetwork::IpNetwork;

use serde::{Deserialize, Serialize};

#[allow(non_snake_case)]
pub mod forwardpb {
    use serde::Serialize;

    tonic::include_proto!("forwardpb");
}

#[allow(non_snake_case)]
pub mod filterpb {
    use serde::Serialize;

    tonic::include_proto!("filterpb");
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
    Delete(DeleteCmd),
    Update(UpdateCmd),
    Show(ShowCmd),
    List(ListCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ListCmd {}

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

impl From<String> for filterpb::Device {
    fn from(n: String) -> Self {
        Self { name: n }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl From<VlanRange> for filterpb::VlanRange {
    fn from(r: VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl From<String> for filterpb::IpNet {
    fn from(value: String) -> Self {
        let parts: Vec<&str> = value.split('/').collect();
        if parts.len() == 1 {
            let addr: IpAddr = value.parse().unwrap();
            return match addr {
                IpAddr::V4(v4) => Self {
                    addr: v4.octets().to_vec(),
                    mask: [0xff, 0xff, 0xff, 0xff].to_vec(),
                },
                IpAddr::V6(v6) => Self {
                    addr: v6.octets().to_vec(),
                    mask: [
                        0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
                    ]
                    .to_vec(),
                },
            };
        }

        if parts.len() != 2 {
            panic!("invalid format");
        }

        let parsed: Result<IpNetwork, _> = value.parse();

        match parsed {
            Ok(net) => Self {
                addr: match net.ip() {
                    IpAddr::V4(v4) => v4.octets().to_vec(),
                    IpAddr::V6(v6) => v6.octets().to_vec(),
                },
                mask: match net.mask() {
                    IpAddr::V4(v4) => v4.octets().to_vec(),
                    IpAddr::V6(v6) => v6.octets().to_vec(),
                },
            },
            Err(_) => {
                let addr: IpAddr = parts[0].parse().unwrap();
                let mask: IpAddr = parts[1].parse().unwrap();
                Self {
                    addr: match addr {
                        IpAddr::V4(v4) => v4.octets().to_vec(),
                        IpAddr::V6(v6) => v6.octets().to_vec(),
                    },
                    mask: match mask {
                        IpAddr::V4(v4) => v4.octets().to_vec(),
                        IpAddr::V6(v6) => v6.octets().to_vec(),
                    },
                }
            }
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
    srcs: Vec<String>,
    dsts: Vec<String>,
}

impl From<ForwardRule> for forwardpb::Rule {
    fn from(forward_rule: ForwardRule) -> Self {
        Self {
            action: Some(forwardpb::Action {
                target: forward_rule.target,
                mode: match forward_rule.mode {
                    ModeKind::None => forwardpb::ForwardMode::None.into(),
                    ModeKind::In => forwardpb::ForwardMode::In.into(),
                    ModeKind::Out => forwardpb::ForwardMode::Out.into(),
                },
                counter: forward_rule.counter,
            }),
            devices: forward_rule.devices.into_iter().map(|m| m.into()).collect(),
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

impl From<ForwardConfig> for Vec<forwardpb::Rule> {
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

pub fn print_config_json(response: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{:}", serde_json::to_string(&response)?);
    Ok(())
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

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
        };
        let response = self.client.show_config(request).await?.into_inner();

        print_config_json(&response)?;

        Ok(())
    }

    pub async fn list_configs(&mut self, _cmd: ListCmd) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        println!("{}", serde_json::to_string_pretty(&response.configs)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?;

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ForwardConfig::from_file(&cmd.rules)?;
        let rules: Vec<forwardpb::Rule> = config.into();
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
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
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::List(cmd) => service.list_configs(cmd).await,
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
