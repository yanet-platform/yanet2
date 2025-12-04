import React, { useMemo } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { InstanceInfo, FunctionInfo } from '../../../api/inspect';
import { SortableDataTable } from '../../../components';
import { compareNullableNumbers, compareNullableStrings } from '../../../utils/sorting';

export interface FunctionsSectionProps {
    instance: InstanceInfo;
}

export const FunctionsSection: React.FC<FunctionsSectionProps> = ({ instance }) => {
    const functionColumns: TableColumnConfig<FunctionInfo>[] = useMemo(() => [
        {
            id: 'name',
            name: 'Name',
            meta: {
                sort: (a: FunctionInfo, b: FunctionInfo) => compareNullableStrings(a.Name, b.Name),
            },
            template: (item: FunctionInfo) => item.Name || '-',
        },
        {
            id: 'chains',
            name: 'Chains',
            meta: {
                sort: (a: FunctionInfo, b: FunctionInfo) =>
                    compareNullableNumbers(a.chains?.length, b.chains?.length),
            },
            template: (item: FunctionInfo) => {
                if (!item.chains || item.chains.length === 0) return '-';
                return item.chains.map(chain => chain.Name || 'unnamed').join(', ');
            },
        },
    ], []);

    return (
        <Box style={{ marginBottom: '24px' }}>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Functions
            </Text>
            {instance.functions && instance.functions.length > 0 ? (
                <SortableDataTable
                    data={instance.functions}
                    columns={functionColumns}
                    width="max"
                />
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No functions</Text>
            )}
        </Box>
    );
};

