import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { toaster } from '../../../utils';
import { API } from '../../../api';
import type { InstanceInfo } from '../../../api/inspect';
import { PageLayout, PageLoader, EmptyState, CommandPaletteHeader } from '../../../components';
import { usePalette } from '../../_shared/command-palette';
import type { Command } from '../../_shared/command-palette';
import { InspectPageFooter } from './InspectPageFooter';
import { InstanceCard } from './InstanceCard';
import './inspect.scss';

const PLACEHOLDER = 'Search or jump to a section…';

/** HUD-style inspect page showing aggregate throughput, devices, modules, pipelines, and functions. */
const InspectPage = (): React.JSX.Element => {
    const [instanceInfo, setInstanceInfo] = useState<InstanceInfo | null>(null);
    const [initialLoading, setInitialLoading] = useState<boolean>(true);
    const [lastUpdate, setLastUpdate] = useState<Date | null>(null);

    const loadInspect = useCallback(async (): Promise<void> => {
        try {
            const data = await API.inspect.inspect();
            setInstanceInfo(data.instance_info ?? null);
            setLastUpdate(new Date());
        } catch (err) {
            toaster.error('inspect-error', 'Failed to fetch inspect data', err);
        } finally {
            setInitialLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInspect();
    }, [loadInspect]);

    const { setPageContribution } = usePalette();

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];

        if (instanceInfo?.devices?.length) {
            list.push({
                id: '__jump_devices',
                icon: '↧',
                label: 'Jump to Devices',
                sub: 'Scroll to the Devices section',
                keywords: 'jump scroll go devices',
                group: 'Jump to section',
                onSelect: () => document.getElementById('iv-section-devices')?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
            });
        }

        if (instanceInfo?.dp_modules?.length) {
            list.push({
                id: '__jump_modules',
                icon: '↧',
                label: 'Jump to Modules',
                sub: 'Scroll to the Modules section',
                keywords: 'jump scroll go modules',
                group: 'Jump to section',
                onSelect: () => document.getElementById('iv-section-modules')?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
            });
        }

        if (instanceInfo?.agents?.length) {
            list.push({
                id: '__jump_agents',
                icon: '↧',
                label: 'Jump to System agents',
                sub: 'Scroll to the System agents section',
                keywords: 'jump scroll go system agents',
                group: 'Jump to section',
                onSelect: () => document.getElementById('iv-section-agents')?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
            });
        }

        if (instanceInfo?.pipelines?.length) {
            list.push({
                id: '__jump_pipelines',
                icon: '↧',
                label: 'Jump to Pipelines',
                sub: 'Scroll to the Pipelines section',
                keywords: 'jump scroll go pipelines',
                group: 'Jump to section',
                onSelect: () => document.getElementById('iv-section-pipelines')?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
            });
        }

        if (instanceInfo?.functions?.length) {
            list.push({
                id: '__jump_functions',
                icon: '↧',
                label: 'Jump to Functions',
                sub: 'Scroll to the Functions section',
                keywords: 'jump scroll go functions',
                group: 'Jump to section',
                onSelect: () => document.getElementById('iv-section-functions')?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
            });
        }

        return list;
    }, [instanceInfo]);

    useEffect(() => {
        setPageContribution({ commands, placeholder: PLACEHOLDER });
        return () => setPageContribution(null);
    }, [commands, setPageContribution]);

    const header = (
        <CommandPaletteHeader title="Inspect" placeholder={PLACEHOLDER} />
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
