import React from 'react';
import { EmptyState } from '../../components';
import { ConfigTabs } from './ConfigTabs';
import type { RouteConfigContentProps } from './types';

export const RouteConfigContent: React.FC<RouteConfigContentProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    getRoutesData,
    onSelectionChange,
    getRouteId,
}) => {

    if (configs.length === 0) {
        return (
            <EmptyState message='No configurations found. Use "Add Route" to create a new configuration.' />
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
