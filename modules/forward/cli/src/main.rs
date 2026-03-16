use core::error::Error;
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use forwardpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    forward_service_client::ForwardServiceClient,
};
use ipnet::IpNet;
use serde::{Deserialize, Serialize};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

#[allow(non_snake_case)]
pub mod forwardpb {
    use serde::Serialize;

    tonic::include_proto!("forwardpb");
}

#[allow(non_snake_case)]
pub mod filterpb {
    use netip::{Ipv4Network, Ipv6Network};
    use serde::{Serialize, Serializer};

    tonic::include_proto!("filterpb");

    impl Serialize for IpNet {
        fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
            match (self.addr.len(), self.mask.len()) {
                (4, 4) => {
                    let addr = u32::from_be_bytes(<[u8; 4]>::try_from(self.mask.as_slice()).expect("checked above"));
                    let mask = u32::from_be_bytes(<[u8; 4]>::try_from(self.mask.as_slice()).expect("checked above"));
                    let net = Ipv4Network::from_bits(addr, mask);
                    s.serialize_str(&net.to_string())
                }
                (16, 16) => {
                    let addr = u128::from_be_bytes(<[u8; 16]>::try_from(self.mask.as_slice()).expect("checked above"));
                    let mask = u128::from_be_bytes(<[u8; 16]>::try_from(self.mask.as_slice()).expect("checked above"));
                    let net = Ipv6Network::from_bits(addr, mask);
                    s.serialize_str(&net.to_string())
                }
                (a, n) => Err(serde::ser::Error::custom(
                    format!("invalid addr/mask lengths: {a}/{n}",),
                )),
            }
        }
    }
}

/// Forward module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
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
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ListCmd {}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete.
    #[arg(long = "cfg", short)]
    pub config: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module config to operate on.
    #[arg(long = "cfg", short)]
    pub config: String,
    /// Ruleset file path.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

impl From<String> for filterpb::Device {
    #[inline]
    fn from(name: String) -> Self {
        Self { name }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct VlanRange {
    from: u32,
    to: u32,
}

impl From<VlanRange> for filterpb::VlanRange {
    fn from(r: VlanRange) -> Self {
        Self { from: r.from, to: r.to }
    }
}

impl TryFrom<String> for filterpb::IpNet {
    type Error = Box<dyn Error>;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        let net: IpNet = value.parse()?;
        let addr = match &net {
            IpNet::V4(v4) => v4.addr().octets().to_vec(),
            IpNet::V6(v6) => v6.addr().octets().to_vec(),
        };
        let mask = match &net {
            IpNet::V4(v4) => v4.netmask().octets().to_vec(),
            IpNet::V6(v6) => v6.netmask().octets().to_vec(),
        };

        Ok(Self { addr, mask })
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ModeKind {
    None,
    In,
    Out,
}

#[derive(Debug, Serialize, Deserialize)]
struct ForwardRule {
    target: String,
    mode: ModeKind,
    counter: String,
    devices: Vec<String>,
    vlan_ranges: Vec<VlanRange>,
    srcs: Vec<String>,
    dsts: Vec<String>,
}

impl TryFrom<ForwardRule> for forwardpb::Rule {
    type Error = Box<dyn Error>;

    fn try_from(forward_rule: ForwardRule) -> Result<Self, Self::Error> {
        Ok(Self {
            action: Some(forwardpb::Action {
                target: forward_rule.target,
                mode: match forward_rule.mode {
                    ModeKind::None => forwardpb::ForwardMode::None.into(),
                    ModeKind::In => forwardpb::ForwardMode::In.into(),
                    ModeKind::Out => forwardpb::ForwardMode::Out.into(),
                },
                counter: forward_rule.counter,
            }),
            devices: forward_rule.devices.into_iter().map(|m| m.into()).collect(),
            vlan_ranges: forward_rule.vlan_ranges.into_iter().map(|m| m.into()).collect(),
            srcs: forward_rule
                .srcs
                .into_iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<Vec<_>, _>>()?,
            dsts: forward_rule
                .dsts
                .into_iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ForwardConfig {
    rules: Vec<ForwardRule>,
}

impl TryFrom<ForwardConfig> for Vec<forwardpb::Rule> {
    type Error = Box<dyn Error>;

    fn try_from(config: ForwardConfig) -> Result<Self, Self::Error> {
        config.rules.into_iter().map(forwardpb::Rule::try_from).collect()
    }
}

impl ForwardConfig {
    pub fn load<P>(path: P) -> Result<Self, Box<dyn Error>>
    where
        P: AsRef<Path>,
    {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;

        Ok(config)
    }
}

pub struct ForwardService {
    client: ForwardServiceClient<LayeredChannel>,
}

impl ForwardService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = ForwardServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self.client.show_config(request).await?.into_inner();

        println!("{}", serde_json::to_string(&response)?);
        Ok(())
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();

        println!("{}", serde_json::to_string(&response.configs)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config.clone() };
        self.client.delete_config(request).await?;

        println!("OK");
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ForwardConfig::load(&cmd.rules)?;
        let rules: Vec<forwardpb::Rule> = config.try_into()?;
        let request = UpdateConfigRequest { name: cmd.config.clone(), rules };
        self.client.update_config(request).await?;

        println!("OK");
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ForwardService::new(&cmd.connection).await?;

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
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
