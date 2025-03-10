//! CLI for YANET "neighbour" module.

use core::{error::Error, time::Duration};
use std::time::UNIX_EPOCH;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{neighbour_client::NeighbourClient, ListNeighboursRequest};
use tabled::{
    settings::{
        object::Columns,
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table,
};
use tonic::transport::Channel;
use yanet_cli_neighbour::{Age, NeighbourEntry, State};
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("routepb");
}

/// Neighbour module.
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
    /// Show current neighbors.
    Show(ShowCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    // NOTE: optional parameters can be added here if needed.
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
    let mut service = NeighbourService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(_) => service.show_neighbours().await,
    }
}

/// Service for interacting with the neighbour module of the YANET router.
///
/// Provides methods to retrieve and display neighbor information through
/// gRPC communication with the control plane.
pub struct NeighbourService {
    client: NeighbourClient<Channel>,
}

impl NeighbourService {
    /// Creates a new NeighbourService connected to the specified endpoint.
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = NeighbourClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    /// Retrieves and displays the current neighbor table in a formatted table.
    pub async fn show_neighbours(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListNeighboursRequest {};
        let response = self.client.list(request).await?.into_inner();

        let mut entries = response
            .neighbours
            .into_iter()
            .map(|entry| {
                let updated_at = UNIX_EPOCH + Duration::from_secs(entry.updated_at as u64);
                let next_hop = entry.next_hop.parse().unwrap();

                NeighbourEntry {
                    next_hop,
                    link_addr: entry.link_addr,
                    hardware_addr: entry.hardware_addr,
                    state: State(entry.state),
                    age: Age(updated_at),
                }
            })
            .collect::<Vec<_>>();

        entries.sort_by(|a, b| (a.state, &a.next_hop).cmp(&(b.state, &b.next_hop)));

        // TODO: in the future we may want to --format=json.
        let mut table = Table::new(entries);
        table.with(
            Style::modern()
                .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
                .remove_frame()
                .remove_horizontal(),
        );
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));

        println!("{}", table);

        Ok(())
    }
}
