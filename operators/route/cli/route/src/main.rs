//! CLI for YANET route operator (route-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's RouteService
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned RIB.

use core::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::IpAddr,
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use netip::{Contiguous, IpNetwork};
use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

use crate::operatorpb::{
    DeleteRouteRequest, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest, LookupRouteRequest, RouteSourceId,
    ShowRoutesRequest, route_service_client::RouteServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

/// Route operator CLI (RIB management).
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all RIB configurations known to the operator.
    List,
    /// Show routes currently stored in RIB.
    Show(RouteShowCmd),
    /// Perform RIB route lookup.
    Lookup(RouteLookupCmd),
    /// Insert a unicast static route.
    Insert(RouteInsertCmd),
    /// Remove a unicast static route.
    Remove(RouteRemoveCmd),
    /// Flush RIB to FIB for a configuration.
    Flush(RouteFlushCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Show only IPv4 routes.
    #[arg(long)]
    pub ipv4: bool,
    /// Show only IPv6 routes.
    #[arg(long)]
    pub ipv6: bool,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteLookupCmd {
    /// IP address to look up.
    pub addr: IpAddr,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Next-hop IP address.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteRemoveCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Next-hop IP address.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteFlushCmd {
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum RouteSource {
    Static,
    Bird,
}

impl RouteSource {
    fn to_proto(&self) -> RouteSourceId {
        match self {
            Self::Static => RouteSourceId::Static,
            Self::Bird => RouteSourceId::Bird,
        }
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
    let mut service = RouteService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_routes(cmd).await,
        ModeCmd::Lookup(cmd) => service.lookup_route(cmd).await,
        ModeCmd::Insert(cmd) => service.insert_route(cmd).await,
        ModeCmd::Remove(cmd) => service.remove_route(cmd).await,
        ModeCmd::Flush(cmd) => service.flush_routes(cmd).await,
    }
}

pub struct RouteService {
    client: RouteServiceClient<LayeredChannel>,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = RouteServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();

        for config in response.configs {
            println!("{config}");
        }
        Ok(())
    }

    pub async fn show_routes(&mut self, cmd: RouteShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowRoutesRequest {
            name: cmd.name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self.client.show_routes(request).await?.into_inner();

        let mut entries = response.routes.into_iter().map(RouteEntry::from).collect::<Vec<_>>();
        entries.sort_by_key(|a| a.prefix.0);

        print_table(entries);
        Ok(())
    }

    pub async fn lookup_route(&mut self, cmd: RouteLookupCmd) -> Result<(), Box<dyn Error>> {
        let request = LookupRouteRequest {
            name: cmd.name.clone(),
            ip_addr: cmd.addr.to_string(),
        };

        let response = self.client.lookup_route(request).await?.into_inner();
        if response.routes.is_empty() {
            log::info!("No routes found for {}", cmd.addr);
            return Ok(());
        }

        print_table(response.routes.into_iter().map(RouteEntry::from));
        Ok(())
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Box<dyn Error>> {
        let request = InsertRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addr: cmd.nexthop_addr.to_string(),
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.client.insert_route(request).await?;
        log::info!(
            "Route inserted successfully: {} via {} (source: {:?})",
            cmd.prefix,
            cmd.nexthop_addr,
            cmd.source
        );
        Ok(())
    }

    pub async fn remove_route(&mut self, cmd: RouteRemoveCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addr: cmd.nexthop_addr.to_string(),
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.client.delete_route(request).await?;
        log::info!(
            "Route removed successfully: {} via {} (source: {:?})",
            cmd.prefix,
            cmd.nexthop_addr,
            cmd.source
        );
        Ok(())
    }

    pub async fn flush_routes(&mut self, cmd: RouteFlushCmd) -> Result<(), Box<dyn Error>> {
        let request = FlushRoutesRequest { name: cmd.name.clone() };
        self.client.flush_routes(request).await?;
        log::info!("Routes flushed successfully");
        Ok(())
    }
}

#[derive(Debug)]
pub struct LargeCommunity {
    pub global_administrator: u32,
    pub local_data_part1: u32,
    pub local_data_part2: u32,
}

impl From<operatorpb::LargeCommunity> for LargeCommunity {
    fn from(community: operatorpb::LargeCommunity) -> Self {
        Self {
            global_administrator: community.global_administrator,
            local_data_part1: community.local_data_part1,
            local_data_part2: community.local_data_part2,
        }
    }
}

impl Display for LargeCommunity {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        write!(
            f,
            "{}:{}:{}",
            self.global_administrator, self.local_data_part1, self.local_data_part2
        )
    }
}

#[derive(Debug)]
pub struct Communities(pub Vec<LargeCommunity>);

impl Display for Communities {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let communities: Vec<String> = self.0.iter().map(|c| c.to_string()).collect();
        write!(f, "{}", communities.join(" "))
    }
}

#[derive(Debug)]
pub struct Prefix(pub Contiguous<IpNetwork>, pub bool);

impl Display for Prefix {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let Prefix(prefix, is_best) = self;
        let prefix = prefix.to_string();
        let prefix = if *is_best {
            prefix.into()
        } else {
            prefix.truecolor(127, 127, 127)
        };
        write!(f, "{prefix}")
    }
}

#[derive(Debug, Tabled)]
pub struct RouteEntry {
    #[tabled(rename = "Prefix")]
    pub prefix: Prefix,
    #[tabled(rename = "Next Hop")]
    pub next_hop: String,
    #[tabled(rename = "Peer")]
    pub peer: String,
    #[tabled(rename = "Source")]
    pub source: String,
    #[tabled(rename = "Peer AS")]
    pub peer_as: u32,
    #[tabled(rename = "Origin")]
    pub origin_as: u32,
    #[tabled(rename = "Pref")]
    pub pref: u32,
    #[tabled(rename = "MED")]
    pub med: u32,
    #[tabled(rename = "Communities")]
    pub communities: Communities,
}

impl From<operatorpb::Route> for RouteEntry {
    fn from(route: operatorpb::Route) -> Self {
        let communities = route.large_communities.into_iter().map(|c| c.into()).collect();
        let prefix = Contiguous::<IpNetwork>::parse(&route.prefix).expect("must be valid prefix");
        let source = RouteSourceId::try_from(route.source)
            .unwrap_or_default()
            .as_str_name()
            .strip_prefix("ROUTE_SOURCE_ID_")
            .unwrap_or_default()
            .to_lowercase();

        Self {
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
    }
}

fn print_table<I, T>(entries: I)
where
    I: IntoIterator<Item = T>,
    T: Tabled,
{
    let mut table = Table::new(entries);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );
    table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
    table.modify(Rows::first(), Color::BOLD);
    println!("{table}");
}
