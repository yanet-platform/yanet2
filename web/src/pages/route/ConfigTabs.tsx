import React, { useState } from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import { VirtualizedRouteTable } from './VirtualizedRouteTable';
import { FIBTable } from './FIBTable';
import type { ConfigTabsProps } from './types';
import type { FIBEntry } from '../../api/routes';
import './route.scss';

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    getRoutesData,
    onSelectionChange,
    getRouteId,
    onEditRoute,
    getFIBEntries,
}) => {
    const validActiveConfig = configs.includes(activeConfig) ? activeConfig : configs[0] || '';
    const [activeSubTab, setActiveSubTab] = useState<string>('rib');

    return (
        <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
            <TabList className="route-config-tabs">
                {configs.map((configName) => (
                    <Tab key={configName} value={configName}>
                        {configName}
                    </Tab>
                ))}
            </TabList>
            <Box className="route-config-tabs__content">
                {configs.map((configName) => {
                    const { routes, selectedIds } = getRoutesData(configName);
                    const fibEntries: FIBEntry[] = getFIBEntries ? getFIBEntries(configName) : [];

                    return (
                        <TabPanel key={configName} value={configName}>
                            <TabProvider value={activeSubTab} onUpdate={setActiveSubTab}>
                                <TabList size="m">
                                    <Tab value="rib">RIB</Tab>
                                    <Tab value="fib">FIB</Tab>
                                </TabList>
                                <Box spacing={{ mt: 2 }}>
                                    <TabPanel value="rib">
                                        <VirtualizedRouteTable
                                            routes={routes}
                                            selectedIds={new Set(selectedIds)}
                                            onSelectionChange={(ids) => onSelectionChange(configName, ids)}
                                            getRouteId={getRouteId}
                                            onEditRoute={onEditRoute}
                                        />
                                    </TabPanel>
                                    <TabPanel value="fib">
                                        <FIBTable entries={fibEntries} />
                                    </TabPanel>
                                </Box>
                            </TabProvider>
                        </TabPanel>
                    );
                })}
            </Box>
        </TabProvider>
    );
};
