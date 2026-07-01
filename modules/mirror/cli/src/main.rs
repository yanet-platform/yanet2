use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use mirrorpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    mirror_service_client::MirrorServiceClient,
};
use netip::{Contiguous, IpNetwork};
use serde::{Deserialize, Serialize};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod mirrorpb {
    use serde::Serialize;

    tonic::include_proto!("modules.mirror.controlplane.mirrorpb.v1");
}

/// Mirror module.
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
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Delete(DeleteCmd),
    Update(UpdateCmd),
    Show(ShowCmd),
    List,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// The name of the module config to show.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete.
    #[arg(long = "name", short = 'n')]
    pub config: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module config to operate on.
    #[arg(long = "name", short = 'n')]
    pub config: String,
    /// Ruleset file path.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl From<VlanRange> for filterpb::pb::VlanRange {
    fn from(r: VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ModeKind {
    None,
    In,
    Out,
}

#[derive(Debug, Serialize, Deserialize)]
struct MirrorRule {
    target: String,
    mode: ModeKind,
    counter: String,
    devices: Vec<String>,
    vlan_ranges: Vec<VlanRange>,
    srcs: Vec<String>,
    dsts: Vec<String>,
}

impl TryFrom<MirrorRule> for mirrorpb::Rule {
    type Error = Box<dyn std::error::Error>;

    fn try_from(mirror_rule: MirrorRule) -> Result<Self, Self::Error> {
        Ok(Self {
            action: Some(mirrorpb::Action {
                target: mirror_rule.target,
                mode: match mirror_rule.mode {
                    ModeKind::None => mirrorpb::MirrorMode::None.into(),
                    ModeKind::In => mirrorpb::MirrorMode::In.into(),
                    ModeKind::Out => mirrorpb::MirrorMode::Out.into(),
                },
                counter: mirror_rule.counter,
            }),
            devices: mirror_rule.devices.into_iter().map(|m| m.into()).collect(),
            vlan_ranges: mirror_rule.vlan_ranges.into_iter().map(Into::into).collect(),
            srcs: mirror_rule
                .srcs
                .into_iter()
                .map(|n| Contiguous::<IpNetwork>::parse(&n).map(filterpb::pb::IpNet::from))
                .collect::<Result<Vec<_>, _>>()?,
            dsts: mirror_rule
                .dsts
                .into_iter()
                .map(|n| Contiguous::<IpNetwork>::parse(&n).map(filterpb::pb::IpNet::from))
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct MirrorConfig {
    rules: Vec<MirrorRule>,
}

impl TryFrom<MirrorConfig> for Vec<mirrorpb::Rule> {
    type Error = Box<dyn std::error::Error>;

    fn try_from(config: MirrorConfig) -> Result<Self, Self::Error> {
        config.rules.into_iter().map(mirrorpb::Rule::try_from).collect()
    }
}

impl MirrorConfig {
    pub fn load<P>(path: P) -> Result<Self, Box<dyn std::error::Error>>
    where
        P: AsRef<Path>,
    {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;

        Ok(config)
    }
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.mirror.controlplane.mirrorpb.v1.MirrorService";

pub struct MirrorService {
    service: Service<MirrorServiceClient<LayeredChannel>>,
}

impl MirrorService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            MirrorServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
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
                serde_yaml::to_string(&response).expect("mirror config YAML serialization must not fail")
            );
        });

        Ok(())
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
            format_args!("no mirror configs"),
            || {
                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted mirror config {}.", cmd.config));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = MirrorConfig::load(&cmd.rules).map_err(|e| self.service.invalid("update", e.to_string()))?;
        let rules: Vec<mirrorpb::Rule> = config
            .try_into()
            .map_err(|e: Box<dyn std::error::Error>| self.service.invalid("update", e.to_string()))?;
        let request = UpdateConfigRequest { name: cmd.config.clone(), rules };
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated mirror config {}.", cmd.config));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = MirrorService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::List => service.list_configs().await,
    }
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
