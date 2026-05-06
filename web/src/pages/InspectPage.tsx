import React, { useCallback, useEffect, useState } from 'react';
import { toaster } from '../utils';
import { API } from '../api';
import type { InstanceInfo } from '../api/inspect';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    InstanceCard,
    InspectPageHeader,
    InspectPageFooter,
} from './inspect';
import './inspect/inspect.scss';

const InspectPage = (): React.JSX.Element => {
    const [instanceInfo, setInstanceInfo] = useState<InstanceInfo | null>(null);
    const [initialLoading, setInitialLoading] = useState<boolean>(true);
    const [refreshing, setRefreshing] = useState<boolean>(false);
    const [lastUpdate, setLastUpdate] = useState<Date | null>(null);

    const loadInspect = useCallback(async (): Promise<void> => {
        try {
            setRefreshing(true);
            const data = await API.inspect.inspect();
            setInstanceInfo(data.instance_info ?? null);
            setLastUpdate(new Date());
        } catch (err) {
            toaster.error('inspect-error', 'Failed to fetch inspect data', err);
        } finally {
            setRefreshing(false);
            setInitialLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInspect();
    }, [loadInspect]);

    return (
        <PageLayout>
            <div className="inspect-page">
                <InspectPageHeader onRefresh={loadInspect} refreshing={refreshing} />
                <div className="inspect-page-body">
                    {initialLoading ? (
                        <PageLoader loading size="l" />
                    ) : !instanceInfo ? (
                        <EmptyState message="No instance data found" />
                    ) : (
                        <InstanceCard instance={instanceInfo} />
                    )}
                </div>
                <InspectPageFooter lastUpdate={lastUpdate} />
            </div>
        </PageLayout>
    );
};

export default InspectPage;
