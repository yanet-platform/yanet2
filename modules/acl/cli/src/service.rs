use std::error::Error;

use tonic::transport::Channel;

use crate::cfg;
use crate::rpc::{self, aclpb, commonpb};

use crate::cmd::{EnableAclCmd, Mode};

////////////////////////////////////////////////////////////////////////////////

pub struct AclService {
    client: rpc::AclServiceClient<Channel>,
}

impl AclService {
    pub async fn connect(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = rpc::AclServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    async fn enable(&mut self, cmd: EnableAclCmd) -> Result<(), Box<dyn Error>> {
        let config = cfg::AclConfig::from_file(&cmd.rules_path)?;
        let rules: Vec<aclpb::Rule> = config.into();
        let request = aclpb::EnableAclRequest {
            rules,
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
        };
        self.client.enable_acl(request).await?;
        log::info!("Successfully enabled acl");
        Ok(())
    }

    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("{mode:?}");
        match mode {
            Mode::Enable(cmd) => self.enable(cmd).await,
        }
    }
}
