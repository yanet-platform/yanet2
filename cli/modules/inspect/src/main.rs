//! CLI for YANET "inspect" module.

use bytesize::ByteSize;
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{inspect_service_client::InspectServiceClient, InspectRequest, InspectResponse};

const INSPECT_SERVICE: &str = "ynpb.InspectService";

/// Inspect module - displays system introspection information.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

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
    let mut service = InspectService::new(&cmd.connection).await?;
    let response = service.inspect().await?;

    output::data(&response, false, format_args!(""), || render_tree(&response));

    Ok(())
}

pub struct InspectService {
    client: InspectServiceClient<LayeredChannel>,
    endpoint: String,
}

impl InspectService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, "inspect", connection.endpoint.clone()))?;
        let client = InspectServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    pub async fn inspect(&mut self) -> Result<InspectResponse, Error> {
        let response = self
            .client
            .inspect(InspectRequest {})
            .await
            .map_err(|status| Error::from_status(status, "inspect", self.endpoint.clone(), INSPECT_SERVICE))?
            .into_inner();

        Ok(response)
    }
}

fn render_tree(response: &InspectResponse) {
    let mut tree = TreeBuilder::new("YANET System".to_string());

    if let Some(info) = &response.instance_info {
        tree.begin_child(format!("Instance {}", info.instance_idx));

        tree.begin_child(format!("Attached to NUMA {}", info.numa_idx));
        tree.end_child();

        tree.begin_child("Dataplane Modules".to_string());
        for (idx, module) in info.dp_modules.iter().enumerate() {
            tree.add_empty_child(format!("{}: {}", idx, module.name));
        }
        tree.end_child();

        tree.begin_child("Controlplane Configurations".to_string());
        for cfg in &info.cp_configs {
            tree.add_empty_child(format!("{}:{} (gen: {})", cfg.r#type, cfg.name, cfg.generation));
        }
        tree.end_child();

        tree.begin_child("Agents".to_string());
        for agent in &info.agents {
            tree.begin_child(agent.name.to_string());

            for instance in &agent.instances {
                let used = instance.memory_limit.saturating_sub(instance.free_bytes);
                tree.begin_child(format!("Instance (PID: {})", instance.pid));
                tree.add_empty_child(format!("Memory limit: {}", ByteSize::b(instance.memory_limit)));
                tree.add_empty_child(format!("Used:         {}", ByteSize::b(used)));
                tree.add_empty_child(format!("Free:         {}", ByteSize::b(instance.free_bytes)));
                tree.add_empty_child(format!("Generation: {}", instance.generation));
                tree.end_child();
            }

            tree.end_child();
        }
        tree.end_child();

        tree.begin_child("Functions".to_string());
        for function in &info.functions {
            tree.begin_child(format!("Function {}", function.name));
            for chain in &function.chains {
                tree.begin_child(format!("Chain {} (weight {})", chain.name, chain.weight));
                for module in &chain.modules {
                    tree.add_empty_child(format!("Module {}:{}", module.r#type, module.name));
                }
                tree.end_child();
            }
            tree.end_child();
        }
        tree.end_child();

        tree.begin_child("Pipelines".to_string());
        for pipeline in &info.pipelines {
            tree.begin_child(format!("Pipeline {}", pipeline.name));
            tree.add_empty_child("rx".to_string());
            for function in &pipeline.functions {
                tree.add_empty_child(function.to_string());
            }
            tree.add_empty_child("tx".to_string());
            tree.end_child();
        }
        tree.end_child();

        tree.begin_child("Devices".to_string());
        for device in &info.devices {
            tree.begin_child(format!("Device {}:{}", device.r#type, device.name));

            tree.begin_child("input".to_string());
            for pipeline in &device.input_pipelines {
                tree.add_empty_child(format!("Pipeline {} (weight: {})", pipeline.name, pipeline.weight));
            }
            tree.end_child();

            tree.begin_child("output".to_string());
            for pipeline in &device.output_pipelines {
                tree.add_empty_child(format!("Pipeline {} (weight: {})", pipeline.name, pipeline.weight));
            }
            tree.end_child();

            tree.end_child();
        }
        tree.end_child();

        tree.end_child();
    }

    let tree = tree.build();
    let _ = ptree::print_tree(&tree);
}
