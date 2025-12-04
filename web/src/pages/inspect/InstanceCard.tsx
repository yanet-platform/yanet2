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

export interface InstanceCardProps {
    instance: InstanceInfo;
}

export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    return (
        <Box
            style={{
                border: '1px solid var(--g-color-line-generic)',
                borderRadius: '8px',
                padding: '20px',
                marginBottom: '20px',
                backgroundColor: 'var(--g-color-base-background)',
            }}
        >
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

