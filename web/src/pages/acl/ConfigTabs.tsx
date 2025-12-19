import React from 'react';
import { TabProvider, TabList, Tab, Text, Box } from '@gravity-ui/uikit';
import type { ConfigTabsProps, ConfigState } from './types';

const getStateIndicator = (state: ConfigState): React.ReactNode => {
    if (state === 'new') {
        return (
            <Text variant="body-1" color="info" style={{ marginLeft: 4 }}>
                (new)
            </Text>
        );
    }
    if (state === 'modified') {
        return (
            <Box
                style={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    backgroundColor: 'var(--g-color-text-warning)',
                    marginLeft: 6,
                }}
            />
        );
    }
    return null;
};

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    configStates,
    onConfigChange,
}) => {
    if (configs.length === 0) {
        return null;
    }

    return (
        <TabProvider value={activeConfig} onUpdate={onConfigChange}>
            <TabList style={{ marginBottom: '8px' }}>
                {configs.map((configName) => {
                    const state = configStates.get(configName) || 'saved';
                    return (
                        <Tab key={configName} value={configName}>
                            <Box style={{ display: 'flex', alignItems: 'center' }}>
                                {configName}
                                {getStateIndicator(state)}
                            </Box>
                        </Tab>
                    );
                })}
            </TabList>
        </TabProvider>
    );
};
