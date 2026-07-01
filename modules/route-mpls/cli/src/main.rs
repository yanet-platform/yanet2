//! CLI for YANET "route-mpls" module.

use core::{net::IpAddr, ops::Deref};

#[allow(non_snake_case)]
pub mod filterpb {
    use serde::Serialize;

    tonic::include_proto!("common.filterpb.v1");
}

#[allow(non_snake_case)]
pub mod routemplspb {
    use serde::Serialize;

    tonic::include_proto!("modules.route_mpls.controlplane.routemplspb.v1");
}

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use netip::{Contiguous, IpNetwork};
use routemplspb::{
    route_mpls_service_client::RouteMplsServiceClient, update_event::Event, CreateConfigRequest, DeleteConfigRequest,
    ListConfigsRequest, NextHop, Rule, ShowConfigRequest, UpdateConfigRequest, UpdateEvent,
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

/// Route module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
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
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteCreateCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteDeleteCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteUpdateCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Route prefix
    #[arg(long = "prefix", short)]
    pub prefix: Contiguous<IpNetwork>,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label")]
    pub mpls_label: u32,
    /// The IP address of the tunnel source.
    #[arg(long = "src")]
    pub src_addr: IpAddr,
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
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Route prefix
    #[arg(long = "prefix", short)]
    pub prefix: Contiguous<IpNetwork>,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label")]
    pub mpls_label: u32,
}

impl TryFrom<Contiguous<IpNetwork>> for filterpb::IpPrefix {
    type Error = Box<dyn std::error::Error>;

    fn try_from(net: Contiguous<IpNetwork>) -> Result<Self, Self::Error> {
        let length = net.prefix() as u32;

        match net.deref() {
            IpNetwork::V4(net) => {
                let addr = net.addr().octets().to_vec();
                Ok(filterpb::IpPrefix { addr, length })
            }
            IpNetwork::V6(net) => {
                let addr = net.addr().octets().to_vec();
                Ok(filterpb::IpPrefix { addr, length })
            }
        }
    }
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService";

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = RouteMplsService::new(&cmd.connection).await?;

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
    service: Service<RouteMplsServiceClient<LayeredChannel>>,
}

impl RouteMplsService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            RouteMplsServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no route-mpls configs"),
            || {
                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: RouteShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(&response, false, format_args!(""), || {
            print!(
                "{}",
                serde_yaml::to_string(&response).expect("route-mpls config YAML serialization must not fail")
            )
        });

        Ok(())
    }

    pub async fn create_config(&mut self, cmd: RouteCreateCmd) -> Result<(), Error> {
        let request = CreateConfigRequest {
            name: cmd.config_name.clone(),
            rules: Vec::<Rule>::new(),
        };
        self.service
            .client()
            .create_config(request)
            .await
            .map_err(self.service.status("create"))?
            .into_inner();

        output::success("create", format_args!("Created route-mpls config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: RouteDeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?
            .into_inner();

        output::success("delete", format_args!("Deleted route-mpls config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn update_route(&mut self, cmd: RouteUpdateCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Update(Rule {
                    prefix: Some(
                        filterpb::IpPrefix::try_from(cmd.prefix)
                            .map_err(|e| self.service.invalid("update", e.to_string()))?,
                    ),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: Some(cmd.src_addr.into()),
                        destination_ip: Some(cmd.dst_addr.into()),
                        weight: cmd.weight,
                        counter: cmd.counter,
                    }),
                })),
            }],
        };
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();

        output::success("update", format_args!("Updated route in {}.", cmd.config_name));

        Ok(())
    }

    pub async fn withdraw_route(&mut self, cmd: RouteWithdrawCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Withdraw(Rule {
                    prefix: Some(
                        filterpb::IpPrefix::try_from(cmd.prefix)
                            .map_err(|e| self.service.invalid("withdraw", e.to_string()))?,
                    ),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: None,
                        destination_ip: Some(cmd.dst_addr.into()),
                        weight: 0,
                        counter: "".to_string(),
                    }),
                })),
            }],
        };
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("withdraw"))?
            .into_inner();

        output::success("withdraw", format_args!("Withdrew route from {}.", cmd.config_name));

        Ok(())
    }
}
