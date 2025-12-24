//! CLI for YANET "route" module.

use core::{error::Error, net::IpAddr};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ipnet::IpNet;
use ptree::TreeBuilder;
use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};
use tonic::{codec::CompressionEncoding, transport::Channel};
use yanet_cli_route::{
    routepb::{
        route_service_client::RouteServiceClient, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest,
        LookupRouteRequest, ShowRoutesRequest,
    },
    RouteEntry,
};
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
    /// Perform RIB route lookup.
    Lookup(RouteLookupCmd),
    /// Inserts a unicast static route.
    Insert(RouteInsertCmd),
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
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteLookupCmd {
    /// The IP address to lookup in the routing table.
    pub addr: IpAddr,
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// The destination prefix of the route.
    ///
    /// The prefix must be an IPv4 or IPv6 address followed by "/" and the
    /// length of the prefix.
    pub prefix: IpNet,
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// The IP address of the nexthop router.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteFlushCmd {
    /// Route config name.
    #[arg(long = "cfg", short)]
    pub config_name: String,
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
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_routes(cmd).await,
        ModeCmd::Lookup(cmd) => service.lookup_route(cmd).await,
        ModeCmd::Insert(cmd) => service.insert_route(cmd).await,
        ModeCmd::Flush(cmd) => service.flush_routes(cmd).await,
    }
}

pub struct RouteService {
    client: RouteServiceClient<Channel>,
}

impl RouteService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = RouteServiceClient::connect(endpoint).await?;
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

        let mut tree = TreeBuilder::new("List Route Configs".to_string());
        for config in response.configs {
            tree.add_empty_child(config);
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }

    pub async fn show_routes(&mut self, cmd: RouteShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowRoutesRequest {
            name: cmd.config_name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self.client.show_routes(request).await?.into_inner();

        let mut entries = response.routes.into_iter().map(RouteEntry::from).collect::<Vec<_>>();

        entries.sort_by(|a, b| a.prefix.0.cmp(&b.prefix.0));

        print_table(entries);

        Ok(())
    }

    pub async fn lookup_route(&mut self, cmd: RouteLookupCmd) -> Result<(), Box<dyn Error>> {
        let request = LookupRouteRequest {
            name: cmd.config_name.clone(),
            ip_addr: cmd.addr.to_string(),
        };

        let response = self.client.lookup_route(request).await?.into_inner();

        if response.routes.is_empty() {
            log::info!("No routes found for {}", cmd.addr);
            return Ok(());
        }

        // NOTE: no sorting here, since routes are already sorted by their best.
        print_table(response.routes.into_iter().map(RouteEntry::from));

        Ok(())
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Box<dyn Error>> {
        let request = InsertRouteRequest {
            name: cmd.config_name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addr: cmd.nexthop_addr.to_string(),
            do_flush: true,
        };

        self.client.insert_route(request).await?;

        log::info!("Route inserted successfully: {} via {}", cmd.prefix, cmd.nexthop_addr);

        Ok(())
    }

    pub async fn flush_routes(&mut self, cmd: RouteFlushCmd) -> Result<(), Box<dyn Error>> {
        let request = FlushRoutesRequest { name: cmd.config_name.clone() };

        self.client.flush_routes(request).await?;

        log::info!("Routes flushed successfully");

        Ok(())
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
