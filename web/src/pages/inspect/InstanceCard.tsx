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
            <Divider style={{ marginBottom: '24px' }} />
            <ConfigurationsSection instance={instance} />
            <Divider style={{ marginBottom: '24px' }} />
            <FunctionsSection instance={instance} />
            <Divider style={{ marginBottom: '24px' }} />
            <PipelinesSection instance={instance} />
            <Divider style={{ marginBottom: '24px' }} />
            <AgentsSection instance={instance} />
            <Divider style={{ marginBottom: '24px' }} />
            <DevicesSection instance={instance} />
        </Box>
    );
};
