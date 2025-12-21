import React from 'react';
import { TabProvider, TabList, Tab, Text, Box } from '@gravity-ui/uikit';
import type { ConfigTabsProps, ConfigState } from './types';
import './acl.css';

const getStateIndicator = (state: ConfigState): React.ReactNode => {
    if (state === 'new') {
        return (
            <Text variant="body-1" color="info" className="acl-config-tabs__state-new">
                (new)
            </Text>
        );
    }
    if (state === 'modified') {
        return <Box className="acl-config-tabs__state-modified" />;
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
            <TabList className="acl-config-tabs">
                {configs.map((configName) => {
                    const state = configStates.get(configName) || 'saved';
                    return (
                        <Tab key={configName} value={configName}>
                            <Box className="acl-config-tabs__tab-content">
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
