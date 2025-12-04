import React from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';

export interface InstanceTabsProps<T> {
    /** Array of items to create tabs for */
    items: T[];
    /** Currently active tab value (string index) */
    activeTab: string;
    /** Handler for tab changes */
    onTabChange: (tab: string) => void;
    /** Function to get the display label for a tab */
    getTabLabel: (item: T, idx: number) => string;
    /** Function to render the content for a tab */
    renderContent: (item: T, idx: number) => React.ReactNode;
    /** Optional style for the tab list container */
    tabListStyle?: React.CSSProperties;
    /** Optional style for the content container */
    contentStyle?: React.CSSProperties;
}

/**
 * Reusable instance tabs component
 */
export const InstanceTabs = <T,>({
    items,
    activeTab,
    onTabChange,
    getTabLabel,
    renderContent,
    tabListStyle = { marginBottom: '20px' },
    contentStyle,
}: InstanceTabsProps<T>): React.JSX.Element => {
    return (
        <TabProvider value={activeTab} onUpdate={onTabChange}>
            <TabList style={tabListStyle}>
                {items.map((item, idx) => (
                    <Tab key={idx} value={String(idx)}>
                        {getTabLabel(item, idx)}
                    </Tab>
                ))}
            </TabList>
            <Box style={contentStyle}>
                {items.map((item, idx) => (
                    <TabPanel key={idx} value={String(idx)}>
                        {renderContent(item, idx)}
                    </TabPanel>
                ))}
            </Box>
        </TabProvider>
    );
};

