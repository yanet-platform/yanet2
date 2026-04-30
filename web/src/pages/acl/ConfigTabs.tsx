import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { ConfigTabs as SharedConfigTabs } from '../../components';
import type { ConfigState, ConfigTabsProps } from './types';
import './acl.scss';

const getStateIndicator = (state: ConfigState): React.ReactNode => {
    if (state === 'new') {
        return (
            <Text
                variant="body-1"
                color="info"
                className="acl-config-tabs__state-new"
            >
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
}) => (
    <SharedConfigTabs
        configs={configs}
        activeConfig={activeConfig}
        onConfigChange={onConfigChange}
        className="acl-config-tabs"
        renderTabLabel={(configName) => (
            <Box className="acl-config-tabs__tab-content">
                {configName}
                {getStateIndicator(configStates.get(configName) ?? 'saved')}
            </Box>
        )}
    />
);
