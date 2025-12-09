//! gRPC service client implementation

use std::error::Error;
use tonic::transport::Channel;

use crate::{
    cmd::*,
    entities::BalancerConfig,
    output,
    rpc::{BalancerClient, balancerpb},
};

////////////////////////////////////////////////////////////////////////////////
// Service
////////////////////////////////////////////////////////////////////////////////

pub struct BalancerService {
    client: BalancerClient<Channel>,
}

impl BalancerService {
    /// Connect to the gRPC endpoint
    pub async fn connect(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = BalancerClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    /// Handle the command
    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("Handling command: {:?}", mode);
        
        match mode {
            Mode::UpdateConfig(cmd) => self.update_config(cmd).await,
            Mode::Reals(cmd) => self.handle_reals(cmd).await,
            Mode::ShowConfig(cmd) => self.show_config(cmd).await,
            Mode::ListConfigs(cmd) => self.list_configs(cmd).await,
            Mode::ConfigStats(cmd) => self.config_stats(cmd).await,
            Mode::StateInfo(cmd) => self.state_info(cmd).await,
            Mode::SessionsInfo(cmd) => self.sessions_info(cmd).await,
        }
    }

    /// Update balancer configuration
    async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Loading configuration from: {}", cmd.config_file);
        
        let config = BalancerConfig::from_yaml_file(&cmd.config_file)?;
        let (module_config, module_state_config) = config.try_into()?;

        let request = balancerpb::UpdateConfigRequest {
            target: Some(crate::rpc::commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
            module_config: Some(module_config),
            module_state_config: Some(module_state_config),
        };

        log::debug!("Sending UpdateConfig request");
        self.client.update_config(request).await?;
        
        log::info!("Successfully updated configuration for '{}' (instance: {})", cmd.name, cmd.instance);
        Ok(())
    }

    /// Handle reals commands
    async fn handle_reals(&mut self, cmd: RealsCmd) -> Result<(), Box<dyn Error>> {
        match cmd.mode {
            RealsMode::Update(cmd) => self.update_real(cmd).await,
            RealsMode::Flush(cmd) => self.flush_real_updates(cmd).await,
        }
    }

    /// Update a real server
    async fn update_real(&mut self, cmd: UpdateRealCmd) -> Result<(), Box<dyn Error>> {
        let action = if cmd.disable { "disable" } else { "enable" };
        log::info!("Buffering {} request for real {} in VS {}:{}/{}", 
            action, cmd.real_ip, cmd.virtual_ip, cmd.virtual_port, cmd.proto);

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;
        
        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;
        
        log::info!("Successfully buffered real update");
        Ok(())
    }

    /// Flush buffered real updates
    async fn flush_real_updates(&mut self, cmd: FlushRealUpdatesCmd) -> Result<(), Box<dyn Error>> {
        log::info!("Flushing buffered real updates for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::FlushRealUpdatesRequest = cmd.into();
        
        log::debug!("Sending FlushRealUpdates request");
        let response = self.client.flush_real_updates(request).await?.into_inner();
        
        log::info!("Successfully flushed {} update(s)", response.updates_flushed);
        Ok(())
    }

    /// Show balancer configuration
    async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching configuration for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::ShowConfigRequest = (&cmd).into();
        let response = self.client.show_config(request).await?.into_inner();
        
        output::print_show_config(&response, cmd.format.into())?;
        Ok(())
    }

    /// List all balancer configurations
    async fn list_configs(&mut self, cmd: ListConfigsCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching all configurations");

        let request = balancerpb::ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        
        output::print_list_configs(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show configuration statistics
    async fn config_stats(&mut self, cmd: ConfigStatsCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching statistics for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::ConfigStatsRequest = (&cmd).into();
        let response = self.client.config_stats(request).await?.into_inner();
        
        output::print_config_stats(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show state information
    async fn state_info(&mut self, cmd: StateInfoCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching state info for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::StateInfoRequest = (&cmd).into();
        let response = self.client.state_info(request).await?.into_inner();
        
        output::print_state_info(&response, cmd.format.into())?;
        Ok(())
    }

    /// Show sessions information
    async fn sessions_info(&mut self, cmd: SessionsInfoCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching sessions info for '{}' (instance: {})", cmd.name, cmd.instance);

        let request: balancerpb::SessionsInfoRequest = (&cmd).into();
        let response = self.client.sessions_info(request).await?.into_inner();
        
        output::print_sessions_info(&response, cmd.format.into())?;
        Ok(())
    }
}