import React, { useMemo, useState } from 'react';
import { DeviceListItem } from './DeviceListItem';
import { IconStack } from './components/Icons';
import type { LocalDevice } from './types';
import type { CounterHistoryEntry } from '../../../hooks/useCounterHistory';
import type { DeviceCounterData } from '../../../hooks/useDeviceCounters';

type FilterKind = 'all' | 'plain' | 'vlan';
type GroupingMode = 'flat' | 'type' | 'parent';

export interface DevicesListProps {
    devices: LocalDevice[];
    selectedDeviceName: string | null;
    grouping: GroupingMode;
    onGroupingChange: (g: GroupingMode) => void;
    onSelectDevice: (deviceName: string) => void;
    counters: Map<string, DeviceCounterData>;
    history: Map<string, CounterHistoryEntry>;
    query: string;
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

const buildGroups = (devices: LocalDevice[], grouping: GroupingMode): DeviceGroup[] => {
    if (grouping === 'type') {
        return [
            { key: 'plain', label: 'Physical', items: devices.filter(d => d.type === 'plain') },
            { key: 'vlan', label: 'VLAN', items: devices.filter(d => d.type === 'vlan') },
        ].filter(g => g.items.length > 0);
    }
    if (grouping === 'parent') {
        // Plain devices each get their own group; all vlans go under one group.
        const groups: DeviceGroup[] = [];
        for (const d of devices) {
            if (d.type === 'plain') {
                groups.push({ key: d.id.name || '', label: d.id.name || '', items: [d] });
            }
        }
        const vlans = devices.filter(d => d.type === 'vlan');
        if (vlans.length > 0) {
            groups.push({ key: 'vlan', label: '∅ orphan VLANs', items: vlans });
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
    query,
}) => {
    const [filter, setFilter] = useState<FilterKind>('all');

    const counts = useMemo(() => ({
        all: devices.length,
        plain: devices.filter(d => d.type === 'plain').length,
        vlan: devices.filter(d => d.type === 'vlan').length,
    }), [devices]);

    const filtered = useMemo(() => {
        const q = query.trim().toLowerCase();
        return devices.filter(d => {
            if (filter === 'plain' && d.type !== 'plain') return false;
            if (filter === 'vlan' && d.type !== 'vlan') return false;
            if (!q) return true;
            const name = (d.id.name || '').toLowerCase();
            const vid = d.vlanId !== undefined ? String(d.vlanId) : '';
            return name.includes(q) || vid.includes(q);
        });
    }, [devices, query, filter]);

    const groups = useMemo(() => buildGroups(filtered, grouping), [filtered, grouping]);

    const chipDefs: [FilterKind, string, number][] = [
        ['all', 'All', counts.all],
        ['plain', 'Physical', counts.plain],
        ['vlan', 'VLAN', counts.vlan],
    ];

    return (
        <div className="dv-list">
            <div className="dv-list-hd">
                <div className="dv-list-counts">
                    {counts.plain} physical · {counts.vlan} vlan
                </div>
                <div className="dv-filter-row">
                    <div className="dv-chips">
                        {chipDefs.map(([k, label, n]) => (
                            <button
                                key={k}
                                className={"dv-chip" + (filter === k ? ' chip-on' : '')}
                                onClick={() => setFilter(k)}
                            >
                                {label} <span className="dv-chip-n">{n}</span>
                            </button>
                        ))}
                    </div>
                    <button
                        className="dv-group-btn"
                        onClick={() => onGroupingChange(nextGrouping(grouping))}
                        title="Toggle grouping"
                    >
                        <IconStack /> {grouping}
                    </button>
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
