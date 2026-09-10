//! CLI for the YANET core services.

use bytesize::ByteSize;
use clap::{CommandFactory, Parser, Subcommand, ValueEnum};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    display::{self, print_table_from_entries},
    errors::Error,
    output,
};
use ynpb::pb::{
    ArenaInfo, ExtendAgentRequest, GetLevelRequest, ListArenasRequest, UpdateLevelRequest,
    logging_client::LoggingClient, memory_service_client::MemoryServiceClient,
};

const LOGGING_SERVICE: &str = "controlplane.ynpb.v1.Logging";

const MEMORY_SERVICE: &str = "controlplane.ynpb.v1.MemoryService";

fn memory_client(channel: LayeredChannel) -> MemoryServiceClient<LayeredChannel> {
    MemoryServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages the control plane process (`yanet-controlplane`): its log level and
/// the shared memory its agents run on.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
struct Cmd {
    #[command(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Subcommand)]
enum ModeCmd {
    /// Manage the log level of the control plane process, covering its Go
    /// logger and the hosted C libraries but not the dataplane.
    #[clap(subcommand)]
    Logging(LoggingCmd),
    /// Manage the shared memory the agents run on.
    #[clap(subcommand)]
    Memory(MemoryCmd),
}

#[derive(Debug, Clone, Parser)]
enum LoggingCmd {
    /// Set the control plane's minimum log level.
    SetLevel(SetLogLevelCmd),
    /// Show the control plane's current minimum log level.
    Show,
}

#[derive(Debug, Clone, Parser)]
struct SetLogLevelCmd {
    /// Minimum log level.
    level: LogLevel,
}

#[derive(Debug, Clone, Subcommand)]
enum MemoryCmd {
    /// List the memory every agent generation holds.
    List,
    /// Grow the memory of a live agent.
    Extend(ExtendCmd),
}

#[derive(Debug, Clone, Parser)]
struct ExtendCmd {
    /// Name of the agent to grow.
    #[arg(add = ArgValueCandidates::new(agent_candidates))]
    agent: String,
    /// How much memory to add, for example 64MiB.
    #[arg(long, value_parser = parse_size)]
    size: ByteSize,
}

fn parse_size(raw: &str) -> Result<ByteSize, String> {
    raw.parse()
        .map_err(|_| format!("expected a size such as 64MiB, got {raw:?}"))
}

/// Log level for the logging service.
#[derive(Debug, Clone, ValueEnum)]
enum LogLevel {
    Debug,
    Info,
    Warn,
    Error,
}

impl From<LogLevel> for ynpb::pb::LogLevel {
    fn from(level: LogLevel) -> Self {
        match level {
            LogLevel::Debug => Self::Debug,
            LogLevel::Info => Self::Info,
            LogLevel::Warn => Self::Warn,
            LogLevel::Error => Self::Error,
        }
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

impl LoggingCmd {
    pub fn action(&self) -> &'static str {
        match self {
            LoggingCmd::SetLevel(..) => "set-level",
            LoggingCmd::Show => "show",
        }
    }
}

impl MemoryCmd {
    pub fn action(&self) -> &'static str {
        match self {
            MemoryCmd::List => "list",
            MemoryCmd::Extend(..) => "extend",
        }
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    match cmd.mode {
        ModeCmd::Logging(mode) => run_logging(&cmd.globals.connection, mode).await,
        ModeCmd::Memory(mode) => run_memory(&cmd.globals.connection, mode).await,
    }
}

async fn run_logging(connection: &ConnectionArgs, mode: LoggingCmd) -> Result<(), Error> {
    let action = mode.action();
    let mut service = Service::connect_for(connection, action, LOGGING_SERVICE, |channel| {
        LoggingClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    match mode {
        LoggingCmd::SetLevel(cmd) => {
            let request = UpdateLevelRequest {
                level: ynpb::pb::LogLevel::from(cmd.level.clone()).into(),
            };
            service
                .unary(action, request, async |client, request| {
                    client.update_level(request).await
                })
                .await?;

            let level_name = cmd.level.to_possible_value().expect("no skipped variants");
            output::success(
                action,
                format_args!("Set the control plane log level to '{}'.", level_name.get_name()),
            );
        }
        LoggingCmd::Show => {
            let response = service
                .unary(action, GetLevelRequest {}, async |client, request| {
                    client.get_level(request).await
                })
                .await?;

            output::data(
                || &response,
                || {
                    display::KeyValue::new()
                        .row("level", ynpb::log_level_name(response.level))
                        .print()
                },
            );
        }
    }

    Ok(())
}

async fn run_memory(connection: &ConnectionArgs, mode: MemoryCmd) -> Result<(), Error> {
    let action = mode.action();
    let mut service = Service::connect_for(connection, action, MEMORY_SERVICE, memory_client).await?;

    match mode {
        MemoryCmd::List => {
            let response = service
                .unary(action, ListArenasRequest {}, async |client, request| {
                    client.list_arenas(request).await
                })
                .await?;

            output::data(
                || &response.arenas,
                || {
                    if response.arenas.is_empty() {
                        output::empty(format_args!("No agents found."));
                        return;
                    }

                    print_table_from_entries(arena_rows(&response.arenas));
                },
            );
        }
        MemoryCmd::Extend(cmd) => {
            let request = ExtendAgentRequest {
                agent: cmd.agent.clone(),
                size: cmd.size.as_u64(),
            };
            let response = service
                .unary_with(
                    action,
                    request,
                    service.not_found(action, &format!("agent '{}'", cmd.agent)),
                    async |client, request| client.extend_agent(request).await,
                )
                .await?;

            output::success(
                action,
                format_args!(
                    "Agent '{}' now holds {}.",
                    cmd.agent,
                    ByteSize::b(response.memory_limit)
                ),
            );
        }
    }

    Ok(())
}

/// Orders each agent's generations newest first; a tie keeps the order the
/// service sent, so the live arena stays ahead of the ones it replaced.
fn arena_rows(arenas: &[ArenaInfo]) -> Vec<ArenaRow> {
    let mut rows: Vec<ArenaRow> = arenas.iter().map(ArenaRow::from).collect();
    rows.sort_by(|left, right| {
        left.agent
            .cmp(&right.agent)
            .then(right.generation.cmp(&left.generation))
    });
    rows
}

#[derive(Debug, Tabled)]
struct ArenaRow {
    #[tabled(rename = "Agent")]
    agent: String,
    #[tabled(rename = "Generation")]
    generation: u64,
    #[tabled(rename = "PID")]
    pid: u32,
    #[tabled(rename = "Limit")]
    limit: String,
    #[tabled(rename = "Free")]
    free: String,
    #[tabled(rename = "Retired")]
    retired: String,
}

impl From<&ArenaInfo> for ArenaRow {
    fn from(arena: &ArenaInfo) -> Self {
        Self {
            agent: arena.agent.clone(),
            generation: arena.generation,
            pid: arena.pid,
            limit: ByteSize::b(arena.memory_limit).to_string(),
            free: ByteSize::b(arena.free_bytes).to_string(),
            retired: if arena.retired { "yes" } else { "no" }.to_owned(),
        }
    }
}

fn agent_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, memory_client, async move |mut client| {
        let mut names: Vec<String> = client
            .list_arenas(ListArenasRequest {})
            .await?
            .into_inner()
            .arenas
            .into_iter()
            .filter(|arena| !arena.retired)
            .map(|arena| arena.agent)
            .collect();
        names.sort();
        names.dedup();

        Ok(names)
    })
}
