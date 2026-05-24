import React, { useCallback, useEffect, useState } from 'react';
import { toaster } from '../../../utils';
import { API } from '../../../api';
import type { InstanceInfo } from '../../../api/inspect';
import { PageLayout, PageLoader, EmptyState } from '../../../components';
import { InspectPageHeader } from './InspectPageHeader';
import { InspectPageFooter } from './InspectPageFooter';
import { InstanceCard } from './InstanceCard';
import './inspect.scss';

/** HUD-style inspect page showing aggregate throughput, devices, modules, pipelines, and functions. */
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

    const header = (
        <InspectPageHeader onRefresh={loadInspect} refreshing={refreshing} />
    );

    return (
        <PageLayout header={header}>
            <div className="inspect-page">
                {initialLoading ? (
                    <PageLoader loading size="l" />
                ) : !instanceInfo ? (
                    <EmptyState message="No instance data found" />
                ) : (
                    <InstanceCard instance={instanceInfo} />
                )}
                <InspectPageFooter lastUpdate={lastUpdate} />
            </div>
        </PageLayout>
    );
};

export default InspectPage;
