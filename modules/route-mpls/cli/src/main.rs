//! CLI for YANET "route-mpls" module.

use core::{error::Error, net::IpAddr};

#[allow(non_snake_case)]
pub mod filterpb {
    use serde::Serialize;

    tonic::include_proto!("filterpb");
}

#[allow(non_snake_case)]
pub mod routemplspb {
    use serde::Serialize;

    tonic::include_proto!("routemplspb");
}

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ipnetwork::IpNetwork;
use routemplspb::{
    route_mpls_service_client::RouteMplsServiceClient, update_event::Event, CreateConfigRequest, DeleteConfigRequest,
    ListConfigsRequest, NextHop, Rule, ShowConfigRequest, UpdateConfigRequest, UpdateEvent,
};
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

/// Route module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all route configurations.
    List,
    /// Show routes currently stored in RIB (route information base).
    Show(RouteShowCmd),
    /// Create route mpls config
    Create(RouteCreateCmd),
    /// Delete route mpls config
    Delete(RouteDeleteCmd),
    /// Update route
    Update(RouteUpdateCmd),
    /// Withdraw route
    Withdraw(RouteWithdrawCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteCreateCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteDeleteCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteUpdateCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Route prefix
    #[arg(long = "prefix", short)]
    pub prefix: IpNetwork,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label")]
    pub mpls_label: u32,
    /// The IP address of the tunnel source.
    #[arg(long = "src")]
    pub src_addr: IpAddr,
    /// Local preference
    #[arg(long = "local_pref")]
    pub local_pref: u32,
    /// AS Path
    #[arg(long = "as_path")]
    pub as_path: Vec<u32>,
    /// MED
    #[arg(long = "med")]
    pub med: u32,
    /// The ECMP weight.
    #[arg(long = "weight")]
    pub weight: u64,
    /// Nexthop counter name
    #[arg(long = "counter")]
    pub counter: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteWithdrawCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Route prefix
    #[arg(long = "prefix", short)]
    pub prefix: IpNetwork,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label")]
    pub mpls_label: u32,
}

impl TryFrom<&IpNetwork> for filterpb::IpPrefix {
    type Error = Box<dyn Error>;

    fn try_from(value: &IpNetwork) -> Result<Self, Self::Error> {
        Ok(match value {
            IpNetwork::V4(v4) => filterpb::IpPrefix {
                addr: v4.ip().octets().to_vec(),
                length: v4.prefix() as u32,
            },
            IpNetwork::V6(v6) => filterpb::IpPrefix {
                addr: v6.ip().octets().to_vec(),
                length: v6.prefix() as u32,
            },
        })
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = RouteMplsService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Create(cmd) => service.create_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_route(cmd).await,
        ModeCmd::Withdraw(cmd) => service.withdraw_route(cmd).await,
    }
}

pub struct RouteMplsService {
    client: RouteMplsServiceClient<Channel>,
}

impl RouteMplsService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = RouteMplsServiceClient::connect(endpoint).await?;
        let client = client
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

    pub async fn show_config(&mut self, cmd: RouteShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self.client.show_config(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response)?);
        Ok(())
    }

    pub async fn create_config(&mut self, cmd: RouteCreateCmd) -> Result<(), Box<dyn Error>> {
        let request = CreateConfigRequest {
            name: cmd.config_name.clone(),
            rules: Vec::<Rule>::new(),
        };
        self.client.create_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: RouteDeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_route(&mut self, cmd: RouteUpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Update(Rule {
                    prefix: Some(filterpb::IpPrefix::try_from(&cmd.prefix)?),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: match cmd.src_addr {
                            IpAddr::V4(v4) => v4.octets().to_vec(),
                            IpAddr::V6(v6) => v6.octets().to_vec(),
                        },
                        destination_ip: match cmd.dst_addr {
                            IpAddr::V4(v4) => v4.octets().to_vec(),
                            IpAddr::V6(v6) => v6.octets().to_vec(),
                        },
                        local_pref: cmd.local_pref,
                        as_path: cmd.as_path,
                        med: cmd.med,
                        weight: cmd.weight,
                        counter: cmd.counter,
                    }),
                })),
            }],
        };
        self.client.update_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn withdraw_route(&mut self, cmd: RouteWithdrawCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Withdraw(Rule {
                    prefix: Some(filterpb::IpPrefix::try_from(&cmd.prefix)?),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: vec![],
                        destination_ip: match cmd.dst_addr {
                            IpAddr::V4(v4) => v4.octets().to_vec(),
                            IpAddr::V6(v6) => v6.octets().to_vec(),
                        },
                        local_pref: 0,
                        as_path: vec![],
                        med: 0,
                        weight: 0,
                        counter: "".to_string(),
                    }),
                })),
            }],
        };
        self.client.update_config(request).await?.into_inner();
        Ok(())
    }
}
