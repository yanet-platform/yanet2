import React, { useEffect, useState } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { toaster } from '../utils';
import { API } from '../api';
import type { InstanceInfo } from '../api/inspect';
import { PageLayout, PageLoader } from '../components';
import { InstanceCard } from './inspect';
import './inspect/inspect.css';

const InspectPage = (): React.JSX.Element => {
    const [instanceInfo, setInstanceInfo] = useState<InstanceInfo | null>(null);
    const [loading, setLoading] = useState<boolean>(true);

    useEffect(() => {
        let isMounted = true;

        const loadInspect = async (): Promise<void> => {
            setLoading(true);

            try {
                const data = await API.inspect.inspect();
                if (!isMounted) return;
                setInstanceInfo(data.instanceInfo || null);
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

    if (!instanceInfo) {
        return (
            <PageLayout title="Inspect">
                <Box className="inspect-page__content">
                    <Text variant="body-1" color="secondary" className="inspect-page__no-data">
                        No instance data found
                    </Text>
                </Box>
            </PageLayout>
        );
    }

    return (
        <PageLayout title="Inspect">
            <Box className="inspect-page__content">
                <InstanceCard instance={instanceInfo} />
            </Box>
        </PageLayout>
    );
};

export default InspectPage;
