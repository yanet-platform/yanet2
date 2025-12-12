import React from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';

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
        <Box style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, height: '100%' }}>
            <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
                <TabList style={{ marginBottom: '16px', flexShrink: 0 }}>
                    {configs.map((config) => (
                        <Tab key={config} value={config}>
                            {config}
                        </Tab>
                    ))}
                </TabList>
                <Box style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', height: '100%' }}>
                    {validActiveConfig && (
                        <TabPanel value={validActiveConfig} style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, height: '100%' }}>
                            {renderContent(validActiveConfig)}
                        </TabPanel>
                    )}
                </Box>
            </TabProvider>
        </Box>
    );
};

