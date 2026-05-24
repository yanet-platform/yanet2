import React, { useCallback, useEffect, useState } from 'react';
import { toaster } from '../../../utils';
import { API } from '../../../api';
import type { InstanceInfo } from '../../../api/inspect';
import { PageLayout, PageLoader, EmptyState } from '../../../components';
import { Inspect3dPageHeader } from './Inspect3dPageHeader';
import { Inspect3dPageFooter } from './Inspect3dPageFooter';
import { InstanceCard } from './InstanceCard';
import './inspect-3d.scss';

/** Inspect (3D) page rendering YANET topology as an isometric three.js scene. */
const Inspect3dPage = (): React.JSX.Element => {
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
            toaster.error('inspect-3d-error', 'Failed to fetch inspect data', err);
        } finally {
            setRefreshing(false);
            setInitialLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInspect();
    }, [loadInspect]);

    const header = (
        <Inspect3dPageHeader onRefresh={loadInspect} refreshing={refreshing} />
    );

    return (
        <PageLayout header={header}>
            <div className="inspect-3d-page">
                {initialLoading ? (
                    <PageLoader loading size="l" />
                ) : !instanceInfo ? (
                    <EmptyState message="No instance data found" />
                ) : (
                    <InstanceCard instance={instanceInfo} />
                )}
                <Inspect3dPageFooter lastUpdate={lastUpdate} />
            </div>
        </PageLayout>
    );
};

export default Inspect3dPage;
