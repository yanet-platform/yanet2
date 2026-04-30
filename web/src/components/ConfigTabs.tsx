import React from 'react';
import { Box, Tab, TabList, TabPanel, TabProvider } from '@gravity-ui/uikit';
import './ConfigTabs.scss';

export interface ConfigTabsProps {
    /** List of config names to render as tabs. */
    configs: string[];
    /** Active config (controlled). Falls back to configs[0] if not in list. */
    activeConfig: string;
    /** Called when the user switches tabs. */
    onConfigChange: (config: string) => void;
    /** Optional content renderer for the active tab. When omitted, only the
     *  tab strip is rendered (for pages that render the panel separately). */
    renderContent?: (configName: string) => React.ReactNode;
    /** Optional custom tab label renderer (e.g. for state indicators). */
    renderTabLabel?: (configName: string) => React.ReactNode;
    /** Extra class on the outer wrapper Box. */
    className?: string;
}

export const ConfigTabs: React.FC<ConfigTabsProps> = ({
    configs,
    activeConfig,
    onConfigChange,
    renderContent,
    renderTabLabel,
    className,
}) => {
    if (configs.length === 0) {
        return null;
    }

    const validActiveConfig = configs.includes(activeConfig)
        ? activeConfig
        : configs[0];

    const wrapperClass = ['config-tabs', className].filter(Boolean).join(' ');

    return (
        <Box className={wrapperClass}>
            <TabProvider value={validActiveConfig} onUpdate={onConfigChange}>
                <TabList className="config-tabs__list">
                    {configs.map((config) => (
                        <Tab key={config} value={config}>
                            {renderTabLabel ? renderTabLabel(config) : config}
                        </Tab>
                    ))}
                </TabList>
                {renderContent && (
                    <Box className="config-tabs__content">
                        <TabPanel
                            value={validActiveConfig}
                            className="config-tabs__panel"
                        >
                            {renderContent(validActiveConfig)}
                        </TabPanel>
                    </Box>
                )}
            </TabProvider>
        </Box>
    );
};
