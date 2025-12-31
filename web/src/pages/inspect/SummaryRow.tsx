import React from 'react';
import { Box } from '@gravity-ui/uikit';
import {
    LayoutCellsLarge,
    Gear,
    ListUl,
    HardDrive,
    CurlyBracketsFunction,
} from '@gravity-ui/icons';
import type { InstanceInfo } from '../../api/inspect';
import { SummaryCard } from './SummaryCard';
import './inspect.scss';

export interface SummaryRowProps {
    instance: InstanceInfo;
}

export const SummaryRow: React.FC<SummaryRowProps> = ({ instance }) => {
    const modulesCount = instance.dpModules?.length ?? 0;
    const configsCount = instance.cpConfigs?.length ?? 0;
    const pipelinesCount = instance.pipelines?.length ?? 0;
    const devicesCount = instance.devices?.length ?? 0;
    const functionsCount = instance.functions?.length ?? 0;

    return (
        <Box className="summary-row">
            <SummaryCard
                icon={LayoutCellsLarge}
                label="Modules"
                value={modulesCount}
                color="info"
            />
            <SummaryCard
                icon={HardDrive}
                label="Devices"
                value={devicesCount}
                color="warning"
            />
            <SummaryCard
                icon={ListUl}
                label="Pipelines"
                value={pipelinesCount}
                color="positive"
            />
            <SummaryCard
                icon={CurlyBracketsFunction}
                label="Functions"
                value={functionsCount}
                color="default"
            />
            <SummaryCard
                icon={Gear}
                label="Configs"
                value={configsCount}
                color="default"
            />
        </Box>
    );
};
