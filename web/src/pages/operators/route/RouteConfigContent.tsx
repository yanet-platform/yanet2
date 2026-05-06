import React from 'react';
import { EmptyState } from '../../../components';
import { ConfigTabs } from './ConfigTabs';
import type { RouteConfigContentProps } from '../../_shared/route/types';

export const RouteConfigContent: React.FC<RouteConfigContentProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    getRoutesData,
    onSelectionChange,
    getRouteId,
    onEditRoute,
}) => {

    if (configs.length === 0) {
        return (
            <EmptyState message='No configurations found. Use "Add Route" to create a new configuration.' />
        );
    }

    return (
        <ConfigTabs
            configs={configs}
            activeConfig={activeConfig}
            onConfigChange={onConfigChange}
            getRoutesData={getRoutesData}
            onSelectionChange={onSelectionChange}
            getRouteId={getRouteId}
            onEditRoute={onEditRoute}
        />
    );
};
