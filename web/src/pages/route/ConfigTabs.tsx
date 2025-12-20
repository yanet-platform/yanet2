import React from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import { VirtualizedRouteTable } from './VirtualizedRouteTable';
import type { ConfigTabsProps } from './types';

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    getRoutesData,
    onSelectionChange,
    getRouteId,
}) => {
    const validActiveConfig = configs.includes(activeConfig) ? activeConfig : configs[0] || '';

    return (
        <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
            <TabList style={{ marginBottom: '16px' }}>
                {configs.map((configName) => (
                    <Tab key={configName} value={configName}>
                        {configName}
                    </Tab>
                ))}
            </TabList>
            <Box style={{ flex: 1, minHeight: 0 }}>
                {configs.map((configName) => {
                    const { routes, selectedIds } = getRoutesData(configName);

                    return (
                        <TabPanel key={configName} value={configName}>
                            <VirtualizedRouteTable
                                routes={routes}
                                selectedIds={new Set(selectedIds)}
                                onSelectionChange={(ids) => onSelectionChange(configName, ids)}
                                getRouteId={getRouteId}
                            />
                        </TabPanel>
                    );
                })}
            </Box>
        </TabProvider>
    );
};
