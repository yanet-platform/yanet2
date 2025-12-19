import React from 'react';
import { TabProvider, TabList, Tab, Box } from '@gravity-ui/uikit';
import type { InnerTabsProps } from './types';

export const InnerTabs: React.FC<InnerTabsProps> = ({
    activeTab,
    onTabChange,
}) => {
    return (
        <Box style={{ marginBottom: '16px' }}>
            <TabProvider value={activeTab} onUpdate={(value) => onTabChange(value as 'rules' | 'fwstate')}>
                <TabList size="m">
                    <Tab value="rules">Rules</Tab>
                    <Tab value="fwstate">FW State Settings</Tab>
                </TabList>
            </TabProvider>
        </Box>
    );
};
