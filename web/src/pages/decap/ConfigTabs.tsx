import React from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import './decap.css';

export interface ConfigTabsProps {
    configs: string[];
    activeConfig: string;
    onConfigChange: (config: string) => void;
    renderContent: (configName: string) => React.ReactNode;
}

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    renderContent,
}) => {
    // Ensure activeConfig is valid
    const validActiveConfig = configs.includes(activeConfig) ? activeConfig : configs[0] || '';

    return (
        <Box className="decap-config-tabs">
            <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
                <TabList className="decap-config-tabs__list">
                    {configs.map((config) => (
                        <Tab key={config} value={config}>
                            {config}
                        </Tab>
                    ))}
                </TabList>
                <Box className="decap-config-tabs__content">
                    {validActiveConfig && (
                        <TabPanel value={validActiveConfig} className="decap-config-tabs__panel">
                            {renderContent(validActiveConfig)}
                        </TabPanel>
                    )}
                </Box>
            </TabProvider>
        </Box>
    );
};

