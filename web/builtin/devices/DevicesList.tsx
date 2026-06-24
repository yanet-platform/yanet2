import React, { useMemo } from 'react';
import { DeviceListItem } from './DeviceListItem';
import { DeviceRail } from './DeviceRail';
import { IconStack, IconCaret } from './components/Icons';
import { deviceTypes } from '@yanet/core/registry';
import type { LocalDevice } from './types';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';

// Active device-type filter: 'all', or a registered device type.
export type FilterKind = string;
type GroupingMode = 'flat' | 'type' | 'parent';

export interface DevicesListProps {
    devices: LocalDevice[];
    selectedDeviceName: string | null;
    grouping: GroupingMode;
    onGroupingChange: (g: GroupingMode) => void;
    onSelectDevice: (deviceName: string) => void;
    counters: Map<string, DeviceCounterData>;
    history: Map<string, CounterHistoryEntry>;
    filter: FilterKind;
    onFilterChange: (filter: FilterKind) => void;
    collapsed: boolean;
    onToggleCollapse: () => void;
}

interface DeviceGroup {
    key: string;
    label: string;
    items: LocalDevice[];
}

const nextGrouping = (current: GroupingMode): GroupingMode => {
    if (current === 'flat') return 'type';
    if (current === 'type') return 'parent';
    return 'flat';
};

export const filterDevices = (devices: LocalDevice[], filter: FilterKind): LocalDevice[] =>
    filter === 'all' ? devices : devices.filter(d => d.type === filter);

export const buildGroups = (devices: LocalDevice[], grouping: GroupingMode): DeviceGroup[] => {
    if (grouping === 'type') {
        return deviceTypes
            .map(m => ({ key: m.type, label: m.pluralLabel, items: devices.filter(d => d.type === m.type) }))
            .filter(g => g.items.length > 0);
    }
    if (grouping === 'parent') {
        // Types with parentMode 'instances' get one group per device; the rest
        // collapse into a single group under their parent label.
        const groups: DeviceGroup[] = [];
        for (const m of deviceTypes) {
            const items = devices.filter(d => d.type === m.type);
            if (items.length === 0) continue;
            if (m.parentMode === 'instances') {
                for (const d of items) {
                    groups.push({ key: d.id.name || '', label: d.id.name || '', items: [d] });
                }
            } else {
                groups.push({ key: m.type, label: m.parentGroupLabel ?? m.pluralLabel, items });
            }
        }
        return groups;
    }
    return [{ key: 'flat', label: '', items: devices }];
};

export const DevicesList: React.FC<DevicesListProps> = ({
    devices,
    selectedDeviceName,
    grouping,
    onGroupingChange,
    onSelectDevice,
    counters,
    history,
    filter,
    onFilterChange,
    collapsed,
    onToggleCollapse,
}) => {

    const counts = useMemo(() => {
        const byType: Record<string, number> = {};
        for (const m of deviceTypes) {
            byType[m.type] = 0;
        }
        for (const d of devices) {
            if (byType[d.type] !== undefined) {
                byType[d.type] += 1;
            }
        }
        return { all: devices.length, byType };
    }, [devices]);

    const filtered = useMemo(() => filterDevices(devices, filter), [devices, filter]);

    const groups = useMemo(() => buildGroups(filtered, grouping), [filtered, grouping]);

    const flatVisible = useMemo(() => groups.flatMap(g => g.items), [groups]);

    const chipDefs: [FilterKind, string, number][] = [
        ['all', 'All', counts.all],
        ...deviceTypes.map(m => [m.type, m.pluralLabel, counts.byType[m.type] ?? 0] as [FilterKind, string, number]),
    ];

    const summary = deviceTypes
        .map(m => `${counts.byType[m.type] ?? 0} ${m.pluralLabel.toLowerCase()}`)
        .join(' · ');

    if (collapsed) {
        return (
            <DeviceRail
                devices={flatVisible}
                selectedDeviceName={selectedDeviceName}
                onSelectDevice={onSelectDevice}
                onExpand={onToggleCollapse}
                counters={counters}
                history={history}
            />
        );
    }

    return (
        <div className="dv-list">
            <div className="dv-list-hd">
                <div className="dv-list-hd-top">
                    <span className="dv-list-counts">{summary}</span>
                    <div className="dv-list-actions">
                        <button
                            className="dv-icon-btn dv-icon-btn-wide"
                            onClick={() => onGroupingChange(nextGrouping(grouping))}
                            title="Toggle grouping"
                        >
                            <IconStack /> {grouping}
                        </button>
                        <button
                            className="dv-icon-btn"
                            onClick={onToggleCollapse}
                            title="Collapse list"
                        >
                            <IconCaret dir="left" />
                        </button>
                    </div>
                </div>
                <div className="dv-filter">
                    {chipDefs.map(([k, label, n]) => (
                        <button
                            key={k}
                            className={"dv-filter-seg" + (filter === k ? ' seg-on' : '')}
                            onClick={() => onFilterChange(k)}
                        >
                            <span className="dv-filter-seg-lbl">{label}</span>
                            <span className="dv-filter-seg-n">{n}</span>
                        </button>
                    ))}
                </div>
            </div>

            <div className="dv-list-scroll">
                {groups.map(g => (
                    <div key={g.key}>
                        {g.label && grouping !== 'flat' && (
                            <div className="dv-grp-hd">
                                <span>{g.label}</span>
                                <span className="dv-grp-n">{g.items.length}</span>
                            </div>
                        )}
                        {g.items.map(d => (
                            <DeviceListItem
                                key={d.id.name}
                                device={d}
                                isSelected={d.id.name === selectedDeviceName}
                                counterData={counters.get(d.id.name || '')}
                                history={history.get(d.id.name || '')}
                                onClick={() => onSelectDevice(d.id.name || '')}
                            />
                        ))}
                    </div>
                ))}
                {filtered.length === 0 && (
                    <div className="dv-empty">No devices match.</div>
                )}
            </div>

            <div className="dv-list-foot">
                <span>{filtered.length} of {devices.length}</span>
            </div>
        </div>
    );
};
