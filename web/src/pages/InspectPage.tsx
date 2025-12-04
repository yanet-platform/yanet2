import React, { useEffect, useState } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { toaster } from '../utils';
import { API } from '../api';
import type { InstanceInfo } from '../api/inspect';
import { PageLayout, PageLoader, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import { InstanceCard } from './inspect';

const InspectPage = (): React.JSX.Element => {
    const [inspectData, setInspectData] = useState<InstanceInfo[]>([]);
    const [loading, setLoading] = useState<boolean>(true);

    const { activeTab, setActiveTab } = useInstanceTabs({ items: inspectData });

    useEffect(() => {
        let isMounted = true;

        const loadInspect = async (): Promise<void> => {
            setLoading(true);

            try {
                const data = await API.inspect.inspect();
                if (!isMounted) return;
                setInspectData(data.instanceInfo || []);
            } catch (err) {
                if (!isMounted) return;
                toaster.error('inspect-error', 'Failed to fetch inspect data', err);
            } finally {
                if (isMounted) {
                    setLoading(false);
                }
            }
        };

        loadInspect();

        return () => {
            isMounted = false;
        };
    }, []);

    if (loading) {
        return (
            <PageLayout title="Inspect">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (inspectData.length === 0) {
        return (
            <PageLayout title="Inspect">
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <Text variant="body-1" color="secondary" style={{ display: 'block' }}>
                        No instances found
                    </Text>
                </Box>
            </PageLayout>
        );
    }

    return (
        <PageLayout title="Inspect">
            <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                <InstanceTabs
                    items={inspectData}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(instance, idx) => `Instance ${instance.instanceIdx ?? idx}`}
                    renderContent={(instance) => <InstanceCard instance={instance} />}
                />
            </Box>
        </PageLayout>
    );
};

export default InspectPage;
