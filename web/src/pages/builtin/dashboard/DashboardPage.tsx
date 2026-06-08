import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { toaster } from '../../../utils';
import { API } from '../../../api';
import type { InstanceInfo } from '../../../api/inspect';
import { PageLoader, EmptyState } from '../../../components';
import type { PagePaletteContribution } from '../../../components/command-palette';
import { usePageContribution } from '../../../hooks';
import { InstanceCard } from './InstanceCard';
import './dashboard.scss';

/** Dashboard page rendering YANET topology as an isometric three.js scene. */
const DashboardPage = (): React.JSX.Element => {
    const [instanceInfo, setInstanceInfo] = useState<InstanceInfo | null>(null);
    const [initialLoading, setInitialLoading] = useState<boolean>(true);

    const loadInspect = useCallback(async (): Promise<void> => {
        try {
            const data = await API.inspect.inspect();
            setInstanceInfo(data.instance_info ?? null);
        } catch (err) {
            toaster.error('dashboard-error', 'Failed to fetch inspect data', err);
        } finally {
            setInitialLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInspect();
    }, [loadInspect]);

    const contribution = useMemo<PagePaletteContribution>(() => ({
        placeholder: 'Search or jump to a page…',
        commands: [
            {
                id: '__reload_dashboard',
                icon: '⟳',
                label: 'Reload dashboard',
                keywords: 'reload refresh dashboard',
                onSelect: () => { loadInspect(); },
            },
        ],
        shortcuts: [
            {
                title: 'Dashboard',
                items: [
                    { keys: '⌘K', desc: 'Search or jump to a page' },
                ],
            },
        ],
    }), [loadInspect]);
    usePageContribution(contribution);

    return (
        <div className="dashboard-page">
            {initialLoading ? (
                <PageLoader loading size="l" />
            ) : !instanceInfo ? (
                <EmptyState message="No instance data found" />
            ) : (
                <InstanceCard instance={instanceInfo} />
            )}
        </div>
    );
};

export default DashboardPage;
