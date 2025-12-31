import React, { useMemo } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { Gear } from '@gravity-ui/icons';
import type { InstanceInfo, CPConfigInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareBigIntValues, compareNullableStrings } from '../../../utils/sorting';
import { InspectSection } from '../InspectSection';
import { formatUint64 } from '../utils';
import '../inspect.scss';

export interface ConfigurationsSectionProps {
    instance: InstanceInfo;
}

export const ConfigurationsSection: React.FC<ConfigurationsSectionProps> = ({ instance }) => {
    const configs = instance.cpConfigs ?? [];

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
        <InspectSection
            title="Controlplane Configurations"
            icon={Gear}
            count={configs.length}
            variant="configs"
            collapsible
            defaultExpanded
        >
            {configs.length > 0 ? (
                <Box className="configs-table-wrapper">
                    <SortableDataTable
                        data={configs}
                        columns={cpConfigColumns}
                        width="max"
                    />
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No configurations
                </Text>
            )}
        </InspectSection>
    );
};
