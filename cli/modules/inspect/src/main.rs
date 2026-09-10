//! CLI for YANET "inspect" module.

mod memory;
mod report;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{InspectRequest, InspectResponse, inspect_service_client::InspectServiceClient};

use crate::report::{Section, View};

/// The fully-qualified gRPC service name used in error messages.
const INSPECT_SERVICE: &str = "controlplane.ynpb.v1.InspectService";

/// Displays the dataplane introspection report.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Report only these sections, repeat the flag for several.
    #[arg(long, value_enum)]
    pub only: Vec<Section>,
    /// Report only this agent's memory.
    #[arg(long, add = ArgValueCandidates::new(agent_candidates))]
    pub agent: Option<String>,
    /// Include detailed memory contexts in human output.
    #[arg(long)]
    pub memory: bool,
    /// Show memory contexts, printing at most this many levels of each
    /// agent's tree.
    #[arg(long)]
    pub depth: Option<usize>,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = InspectService::new(&cmd.connection).await?;
    let mut response = service.inspect().await?;

    let view = View::new(cmd.only, cmd.memory, cmd.depth);
    select(&mut response, &view, cmd.agent.as_deref());

    output::data(|| &response, || report::render(&response, &view));

    Ok(())
}

/// Drops everything the selectors leave out, so that both renderings report
/// the same subset of the instance.
fn select(response: &mut InspectResponse, view: &View, agent: Option<&str>) {
    let Some(info) = response.instance_info.as_mut() else {
        return;
    };

    if let Some(agent) = agent {
        info.agents.retain(|candidate| candidate.name == agent);
    }

    if !view.shows(Section::Memory) {
        info.agents.clear();
    }
    if !view.shows(Section::Device) {
        info.devices.clear();
    }
    if !view.shows(Section::Pipeline) {
        info.pipelines.clear();
    }
    if !view.shows(Section::Function) {
        info.functions.clear();
    }
    if !view.shows(Section::Module) {
        info.dp_modules.clear();
        info.cp_configs.clear();
    }
}

fn client(channel: LayeredChannel) -> InspectServiceClient<LayeredChannel> {
    InspectServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

pub struct InspectService {
    service: Service<InspectServiceClient<LayeredChannel>>,
}

impl InspectService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "inspect", INSPECT_SERVICE, client).await?;

        Ok(Self { service })
    }

    pub async fn inspect(&mut self) -> Result<InspectResponse, Error> {
        self.service
            .unary("inspect", InspectRequest {}, async |client, request| {
                client.inspect(request).await
            })
            .await
    }
}

/// Completion candidates for an `--agent` argument: the agents the instance
/// currently reports.
///
/// Strictly best-effort — see [`completion::candidates`].
fn agent_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        let response = client.inspect(InspectRequest {}).await?.into_inner();

        let mut names: Vec<String> = response
            .instance_info
            .into_iter()
            .flat_map(|info| info.agents)
            .map(|agent| agent.name)
            .collect();
        names.sort();
        names.dedup();

        Ok(names)
    })
}

#[cfg(test)]
mod test {
    use ynpb::pb::{AgentInfo, DeviceInfo, InstanceInfo, PipelineInfo};

    use super::*;

    /// Returns an instance carrying one agent, device and pipeline, so that
    /// dropping one section leaves the others observable.
    fn response(agents: &[&str]) -> InspectResponse {
        InspectResponse {
            instance_info: Some(InstanceInfo {
                instance_idx: 0,
                numa_idx: 0,
                dp_modules: Vec::new(),
                cp_configs: Vec::new(),
                functions: Vec::new(),
                pipelines: vec![PipelineInfo {
                    name: "forward".to_owned(),
                    functions: Vec::new(),
                }],
                agents: agents
                    .iter()
                    .map(|name| AgentInfo {
                        name: (*name).to_owned(),
                        instances: Vec::new(),
                    })
                    .collect(),
                devices: vec![DeviceInfo {
                    r#type: "plain".to_owned(),
                    name: "port0".to_owned(),
                    input_pipelines: Vec::new(),
                    output_pipelines: Vec::new(),
                    index: 0,
                }],
            }),
        }
    }

    /// Returns the agent names left in an inspection response.
    fn agent_names(response: &InspectResponse) -> Vec<&str> {
        response
            .instance_info
            .iter()
            .flat_map(|info| &info.agents)
            .map(|agent| agent.name.as_str())
            .collect()
    }

    #[test]
    fn test_select_without_selectors_keeps_everything() {
        let mut response = response(&["route", "acl"]);

        select(&mut response, &View::new(Vec::new(), false, None), None);

        let info = response.instance_info.as_ref().unwrap();
        assert_eq!(vec!["route", "acl"], agent_names(&response));
        assert_eq!(1, info.devices.len());
        assert_eq!(1, info.pipelines.len());
    }

    #[test]
    fn test_select_keeps_only_the_named_agent() {
        let mut response = response(&["route", "acl"]);

        select(&mut response, &View::new(Vec::new(), false, None), Some("acl"));

        assert_eq!(vec!["acl"], agent_names(&response));
    }

    #[test]
    fn test_select_unknown_agent_leaves_no_agents() {
        let mut response = response(&["route"]);

        select(&mut response, &View::new(Vec::new(), false, None), Some("missing"));

        assert!(agent_names(&response).is_empty());
    }

    #[test]
    fn test_select_drops_unselected_sections() {
        let mut response = response(&["route"]);

        select(&mut response, &View::new(vec![Section::Memory], false, None), None);

        let info = response.instance_info.as_ref().unwrap();
        assert_eq!(vec!["route"], agent_names(&response));
        assert!(info.devices.is_empty());
        assert!(info.pipelines.is_empty());
    }

    #[test]
    fn test_select_keeps_every_named_section() {
        let mut response = response(&["route"]);

        select(
            &mut response,
            &View::new(vec![Section::Device, Section::Pipeline], false, None),
            None,
        );

        let info = response.instance_info.as_ref().unwrap();
        assert!(agent_names(&response).is_empty());
        assert_eq!(1, info.devices.len());
        assert_eq!(1, info.pipelines.len());
    }
}
