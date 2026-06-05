import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { toaster } from '../../../utils';
import { API } from '../../../api';
import type { InstanceInfo } from '../../../api/inspect';
import { PageLayout, PageLoader, EmptyState, CommandPaletteHeader } from '../../../components';
import { usePalette } from '../../../components/command-palette';
import type { Command } from '../../../components/command-palette';
import { InspectPageFooter } from './InspectPageFooter';
import { InstanceCard } from './InstanceCard';
import './inspect.scss';

const PLACEHOLDER = 'Search or jump to a section…';

const JUMP_SECTIONS: ReadonlyArray<{
    key: string;
    label: string;
    anchor: string;
    has: (info: InstanceInfo) => boolean;
}> = [
    { key: 'devices',   label: 'Devices',       anchor: 'iv-section-devices',   has: (info) => !!info.devices?.length },
    { key: 'modules',   label: 'Modules',        anchor: 'iv-section-modules',   has: (info) => !!info.dp_modules?.length },
    { key: 'agents',    label: 'System agents',  anchor: 'iv-section-agents',    has: (info) => !!info.agents?.length },
    { key: 'pipelines', label: 'Pipelines',      anchor: 'iv-section-pipelines', has: (info) => !!info.pipelines?.length },
    { key: 'functions', label: 'Functions',      anchor: 'iv-section-functions', has: (info) => !!info.functions?.length },
];

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
        if (!instanceInfo) {
            return [];
        }
        const info = instanceInfo;
        return JUMP_SECTIONS.filter((section) => section.has(info)).map((section) => ({
            id: `__jump_${section.key}`,
            icon: '↧',
            label: `Jump to ${section.label}`,
            sub: `Scroll to the ${section.label} section`,
            keywords: `jump scroll go ${section.label.toLowerCase()}`,
            group: 'Jump to section',
            onSelect: () => document.getElementById(section.anchor)?.scrollIntoView({ block: 'start', behavior: 'smooth' }),
        }));
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
