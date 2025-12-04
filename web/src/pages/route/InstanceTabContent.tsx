import React from 'react';
import { EmptyState } from '../../components';
import { VirtualizedRouteTable } from './VirtualizedRouteTable';
import { ConfigTabs } from './ConfigTabs';
import type { InstanceTabContentProps } from './types';

export const InstanceTabContent: React.FC<InstanceTabContentProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    getRoutesData,
    onSelectionChange,
    getRouteId,
}) => {

    if (configs.length === 0) {
        return (
            <EmptyState message='No configurations found for this instance. Use "Add Route" to create a new configuration.' />
        );
    }

    // Single config - render table directly without tabs
    if (configs.length === 1) {
        const configName = configs[0];
        const { routes, selectedIds } = getRoutesData(configName);

        return (
            <VirtualizedRouteTable
                routes={routes}
                selectedIds={new Set(selectedIds)}
                onSelectionChange={(ids) => onSelectionChange(configName, ids)}
                getRouteId={getRouteId}
            />
        );
    }

    // Multiple configs - use tabs
    return (
        <ConfigTabs
            configs={configs}
            activeConfig={activeConfig}
            onConfigChange={onConfigChange}
            getRoutesData={getRoutesData}
            onSelectionChange={onSelectionChange}
            getRouteId={getRouteId}
        />
    );
};
