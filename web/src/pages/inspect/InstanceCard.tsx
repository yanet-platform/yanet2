import React from 'react';
import { Box } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../api/inspect';
import { SummaryRow } from './SummaryRow';
import {
    ModulesSection,
    ConfigurationsSection,
    FunctionsSection,
    PipelinesSection,
    AgentsSection,
    DevicesSection,
} from './sections';
import './inspect.scss';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    return (
        <Box className="instance-card">
            <SummaryRow instance={instance} />

            <ModulesSection instance={instance} />

            <DevicesSection instance={instance} />

            <Box className="instance-card__grid">
                <PipelinesSection instance={instance} />
                <FunctionsSection instance={instance} />
            </Box>

            <Box className="instance-card__grid">
                <AgentsSection instance={instance} />
                <ConfigurationsSection instance={instance} />
            </Box>
        </Box>
    );
};
