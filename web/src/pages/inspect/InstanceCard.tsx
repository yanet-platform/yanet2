import React from 'react';
import { Box, Divider } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../api/inspect';
import {
    ModulesSection,
    ConfigurationsSection,
    FunctionsSection,
    PipelinesSection,
    AgentsSection,
    DevicesSection,
} from './sections';
import './inspect.css';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    return (
        <Box className="instance-card">
            <ModulesSection instance={instance} />
            <Divider />
            <ConfigurationsSection instance={instance} />
            <Divider />
            <FunctionsSection instance={instance} />
            <Divider />
            <PipelinesSection instance={instance} />
            <Divider />
            <AgentsSection instance={instance} />
            <Divider />
            <DevicesSection instance={instance} />
        </Box>
    );
};
