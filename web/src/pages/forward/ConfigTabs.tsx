import React from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import type { ConfigTabsProps } from './types';
import './forward.css';

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    renderContent,
}) => {
    // Ensure activeConfig is valid
    const validActiveConfig = configs.includes(activeConfig) ? activeConfig : configs[0] || '';

    return (
        <Box className="forward-config-tabs">
            <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
                <TabList className="forward-config-tabs__list">
                    {configs.map((config) => (
                        <Tab key={config} value={config}>
                            {config}
                        </Tab>
                    ))}
                </TabList>
                <Box className="forward-config-tabs__content">
                    {validActiveConfig && (
                        <TabPanel value={validActiveConfig} className="forward-config-tabs__panel">
                            {renderContent(validActiveConfig)}
                        </TabPanel>
                    )}
                </Box>
            </TabProvider>
        </Box>
    );
};
