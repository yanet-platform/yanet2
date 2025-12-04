import React, { useMemo } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { InstanceInfo, CPConfigInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareBigIntValues, compareNullableStrings } from '../../../utils/sorting';
import { formatUint64 } from '../utils';

export interface ConfigurationsSectionProps {
    instance: InstanceInfo;
}

export const ConfigurationsSection: React.FC<ConfigurationsSectionProps> = ({ instance }) => {
    const cpConfigColumns: TableColumnConfig<CPConfigInfo>[] = useMemo(() => [
        {
            id: 'type',
            name: 'Type',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) => compareNullableStrings(a.type, b.type),
            },
            template: (item: CPConfigInfo) => item.type || '-',
        },
        {
            id: 'name',
            name: 'Name',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) => compareNullableStrings(a.name, b.name),
            },
            template: (item: CPConfigInfo) => item.name || '-',
        },
        {
            id: 'generation',
            name: 'Generation',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) => compareBigIntValues(a.generation, b.generation),
            },
            template: (item: CPConfigInfo) => formatUint64(item.generation),
        },
    ], []);

    return (
        <Box style={{ marginBottom: '24px' }}>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Controlplane Configurations
            </Text>
            {instance.cpConfigs && instance.cpConfigs.length > 0 ? (
                <SortableDataTable
                    data={instance.cpConfigs}
                    columns={cpConfigColumns}
                    width="max"
                />
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No configurations</Text>
            )}
        </Box>
    );
};

