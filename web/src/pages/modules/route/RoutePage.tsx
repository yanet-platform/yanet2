import React, { useCallback, useEffect, useState } from 'react';
import { Box, TabProvider, TabList, Tab, TabPanel } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState, PageHeader } from '../../../components';
import { useRouteConfigs } from '../../_shared/route/useRouteConfigs';
import { useFIBData } from '../../_shared/route/useFIBData';
import { FIBTable } from './FIBTable';
import '../../_shared/route/route.scss';

const FIBConfigContent: React.FC<{
    configs: string[];
    activeConfig: string;
    onConfigChange: (config: string) => void;
    getFIBEntries: (configName: string) => import('../../../api/routes').FIBEntry[];
    loadFIBEntries: (configName: string) => Promise<import('../../../api/routes').FIBEntry[]>;
}> = ({ configs, activeConfig, onConfigChange, getFIBEntries, loadFIBEntries }) => {
    const validActiveConfig = configs.includes(activeConfig) ? activeConfig : configs[0] || '';
    const [fibLoading, setFibLoading] = useState(false);

    useEffect(() => {
        if (!validActiveConfig) {
            return;
        }
        let cancelled = false;
        setFibLoading(true);
        loadFIBEntries(validActiveConfig).finally(() => {
            if (!cancelled) {
                setFibLoading(false);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [validActiveConfig, loadFIBEntries]);

    const handleConfigChange = useCallback((config: string) => {
        onConfigChange(config);
    }, [onConfigChange]);

    if (configs.length === 0) {
        return <EmptyState message="No configurations found." />;
    }

    return (
        <TabProvider value={validActiveConfig} onUpdate={handleConfigChange}>
            <TabList className="route-config-tabs">
                {configs.map((configName) => (
                    <Tab key={configName} value={configName}>
                        {configName}
                    </Tab>
                ))}
            </TabList>
            <Box className="route-config-tabs__content">
                {configs.map((configName) => {
                    const fibEntries = getFIBEntries(configName);
                    return (
                        <TabPanel key={configName} value={configName}>
                            {fibLoading ? (
                                <Box style={{ padding: '16px', textAlign: 'center' }}>Loading FIB...</Box>
                            ) : (
                                <FIBTable entries={fibEntries} />
                            )}
                        </TabPanel>
                    );
                })}
            </Box>
        </TabProvider>
    );
};

const RoutePage: React.FC = () => {
    const {
        configs,
        loading,
        activeConfigTab,
        handleConfigTabChange,
    } = useRouteConfigs();

    const { configFIB, loadFIBForConfig } = useFIBData(configs);

    const getFIBEntries = useCallback((configName: string) => {
        return configFIB.get(configName) || [];
    }, [configFIB]);

    const headerContent = (
        <PageHeader title="Route FIB" />
    );

    if (loading) {
        return (
            <PageLayout title="Route FIB">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <EmptyState message="No configs found." />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box className="route-page__content">
                <FIBConfigContent
                    configs={configs}
                    activeConfig={activeConfigTab}
                    onConfigChange={handleConfigTabChange}
                    getFIBEntries={getFIBEntries}
                    loadFIBEntries={loadFIBForConfig}
                />
            </Box>
        </PageLayout>
    );
};

export default RoutePage;
