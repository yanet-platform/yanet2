import React, { useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { InstanceInfo, CPConfigInfo } from '../../../api/inspect';
import { SortableDataTable, EmptyState } from '../../../components';
import { compareBigIntValues, compareNullableStrings, formatUint64 } from '../../../utils';
import { InspectCard } from '../InspectCard';

export interface ConfigurationsSectionProps {
    instance: InstanceInfo;
}

export const ConfigurationsSection: React.FC<ConfigurationsSectionProps> = ({ instance }) => {
    const configs = instance.cp_configs ?? [];

    const columns: TableColumnConfig<CPConfigInfo>[] = useMemo(() => [
        {
            id: 'type',
            name: 'Type',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) => compareNullableStrings(a.type, b.type),
            },
            template: (item: CPConfigInfo) => (
                <span className="inspect-mono">{item.type || '-'}</span>
            ),
        },
        {
            id: 'name',
            name: 'Name',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) => compareNullableStrings(a.name, b.name),
            },
            template: (item: CPConfigInfo) => (
                <span className="inspect-mono">{item.name || '-'}</span>
            ),
        },
        {
            id: 'generation',
            name: 'Generation',
            align: 'right',
            meta: {
                sort: (a: CPConfigInfo, b: CPConfigInfo) =>
                    compareBigIntValues(a.generation, b.generation),
            },
            template: (item: CPConfigInfo) => (
                <span className="inspect-mono inspect-muted">{formatUint64(item.generation)}</span>
            ),
        },
    ], []);

    return (
        <InspectCard title="Controlplane configs" count={configs.length}>
            {configs.length > 0 ? (
                <Box className="inspect-table-host">
                    <SortableDataTable data={configs} columns={columns} width="max" />
                </Box>
            ) : (
                <EmptyState message="No configurations" compact />
            )}
        </InspectCard>
    );
};
