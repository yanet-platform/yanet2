//! CLI for YANET "route" module.

use core::{error::Error, net::IpAddr, str::FromStr};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{route_service_client::RouteServiceClient, InsertRouteRequest, RouteSourceId, ShowRoutesRequest};
use ipnet::IpNet;
use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table,
};
use tonic::transport::Channel;
use yanet_cli_route::{Communities, LargeCommunity, Prefix, RouteEntry};
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("routepb");
}

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
    /// Show routes currently stored in RIB (route information base).
    Show(RouteShowCmd),
    /// Inserts a unicast static route.
    Insert(RouteInsertCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Show only IPv4 routes.
    #[arg(long)]
    pub ipv4: bool,
    /// Show only IPv6 routes.
    #[arg(long)]
    pub ipv6: bool,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// The destination prefix of the route.
    ///
    /// The prefix must be an IPv4 or IPv6 address followed by "/" and the
    /// length of the prefix.
    pub prefix: IpNet,
    /// Route module name.
    #[arg(long = "mod")]
    pub module_name: String,
    /// The IP address of the nexthop router.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// NUMA node index where changes should be applied, optionally repeated.
    ///
    /// If not specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,
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
    let mut service = RouteService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_routes(cmd).await,
        ModeCmd::Insert(cmd) => service.insert_route(cmd).await,
    }
}

pub struct RouteService {
    client: RouteServiceClient<Channel>,
}

impl RouteService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = RouteServiceClient::connect(endpoint).await?;
        let m = Self { client };

        Ok(m)
    }

    pub async fn show_routes(&mut self, cmd: RouteShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowRoutesRequest {
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self.client.show_routes(request).await?.into_inner();

        let mut entries = response
            .routes
            .into_iter()
            .map(|route| {
                let communities = route
                    .large_communities
                    .into_iter()
                    .map(|c| LargeCommunity {
                        global_administrator: c.global_administrator,
                        local_data_part1: c.local_data_part1,
                        local_data_part2: c.local_data_part2,
                    })
                    .collect();

                let prefix = IpNet::from_str(&route.prefix).expect("must be valid prefix");

                let source = RouteSourceId::try_from(route.source)
                    .unwrap_or_default()
                    .as_str_name()
                    .strip_prefix("ROUTE_SOURCE_ID_")
                    .unwrap_or_default()
                    .to_lowercase();

                RouteEntry {
                    prefix: Prefix(prefix, route.is_best),
                    next_hop: route.next_hop,
                    peer: route.peer,
                    source,
                    peer_as: route.peer_as,
                    origin_as: route.origin_as,
                    pref: route.pref,
                    med: route.med,
                    communities: Communities(communities),
                }
            })
            .collect::<Vec<_>>();

        entries.sort_by(|a, b| a.prefix.0.cmp(&b.prefix.0));

        let mut table = Table::new(entries);
        table.with(
            Style::modern()
                .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
                .remove_horizontal(),
        );
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);

        println!("{}", table);

        Ok(())
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Box<dyn Error>> {
        let request = InsertRouteRequest {
            module_name: cmd.module_name,
            prefix: cmd.prefix.to_string(),
            nexthop_addr: cmd.nexthop_addr.to_string(),
            numa: cmd.numa.unwrap_or_default(),
        };

        let resp = self.client.insert_route(request).await?;

        log::debug!("InsertRouteResponse: {:?}", resp);
        Ok(())
    }
}
