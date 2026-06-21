import React, { useMemo } from 'react';
import { Icon } from '@gravity-ui/uikit';
import { Pause, Play } from '@gravity-ui/icons';
import type { RuleItem } from './types';
import { expandRuleItem, effectiveCounterName, deadReasonText } from './hooks';
import {
    AnyChip,
    IpNetChip,
    PortRangeChip,
    VlanRangeChip,
    ProtoChip,
    ActionChain,
    ChipList,
} from './chips';
import { ruleTableCommonProps } from '@yanet/core/components/draft';
import type { RuleRate } from './useAclRuleCounters';
import Sparkline from '@yanet/core/components/Sparkline';
import { VirtualTable, type Column } from '@yanet/core/components/VirtualTable';

const COLUMN_WIDTHS = {
    srcs: 180,
    dsts: 180,
    src_ports: 130,
    dst_ports: 130,
    protos: 150,
    vlans: 110,
    devices: 130,
    counter: 140,
    actions: 190,
    sparkline: 210,
} as const;

// Gapless layout: checkbox(38) + index(48) + data-cols sum. No column gaps.
// Data cols: 180+180+130+130+150+110+130+140+190+210 = 1550.
const MIN_WIDTH = 38 + 48 + Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

interface SparklineCellProps {
    item: RuleItem;
    rate: RuleRate | undefined;
    counterEnabled: boolean;
    onToggleCounter: (counterName: string) => void;
}

const SparklineCell: React.FC<SparklineCellProps> = ({ item, rate, counterEnabled, onToggleCounter }) => {
    if (counterEnabled) {
        return (
            <span style={{ display: 'flex', alignItems: 'center', gap: 4, width: '100%' }}>
                {rate ? (
                    <>
                        <Sparkline values={rate.history} width={120} height={22} sizeEmptyToBox />
                        <span className="yn-cell-pps acl-cell-pps" title={`${rate.pps.toFixed(0)} pps`}>
                            {rate.pps >= 1000
                                ? `${(rate.pps / 1000).toFixed(1)}k`
                                : rate.pps.toFixed(0)}
                        </span>
                    </>
                ) : (
                    <>
                        <Sparkline values={null} width={120} height={22} sizeEmptyToBox />
                        <span className="yn-cell-pps acl-cell-pps acl-pps-loading" title="Waiting for counter data">…</span>
                    </>
                )}
                <button
                    type="button"
                    className="acl-counter-toggle acl-counter-toggle--on"
                    onClick={() => onToggleCounter(effectiveCounterName(item.rule, item.index))}
                    title="Stop tracking this counter"
                    aria-label="Disable counter"
                >
                    <Icon data={Pause} size={16} />
                </button>
            </span>
        );
    }

    return (
        <span style={{ display: 'flex', alignItems: 'center', gap: 4, width: '100%' }}>
            <Sparkline values={null} width={120} height={22} sizeEmptyToBox />
            <span className="yn-cell-pps acl-cell-pps" style={{ color: 'var(--yn-text-3)' }}>—</span>
            <button
                type="button"
                className={`acl-counter-toggle acl-counter-toggle--off${!item.counter ? ' acl-counter-toggle--default' : ''}`}
                onClick={() => onToggleCounter(effectiveCounterName(item.rule, item.index))}
                title={`Track counter "${effectiveCounterName(item.rule, item.index)}"`}
                aria-label="Enable counter"
            >
                <Icon data={Play} size={16} />
            </button>
        </span>
    );
};

interface RuleTableProps {
    items: RuleItem[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    flashRowId?: string | null;
    onSelectionChange: (ids: Set<string>) => void;
    onEditRule: (item: RuleItem) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
    rates: Map<string, RuleRate>;
    enabledCounterNames: Set<string>;
    onToggleCounter: (counterName: string) => void;
}

/** Virtualized ACL rule table — thin wrapper over the shared VirtualTable shell. */
const RuleTable: React.FC<RuleTableProps> = ({
    items,
    selectedIds,
    activeRowId,
    flashRowId,
    onSelectionChange,
    onEditRule,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
    rates,
    enabledCounterNames,
    onToggleCounter,
}) => {
    const columns: Column<RuleItem>[] = useMemo(() => [
        {
            key: 'srcs',
            header: 'Sources',
            gridTrack: `${COLUMN_WIDTHS.srcs}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.isEmptySrc
                    ? <span className="yn-cell-mono yn-cell-muted">—</span>
                    : <ChipList
                        items={expanded.sourceCidrs}
                        renderChip={(cidr, idx) => <IpNetChip key={idx} cidr={cidr} />}
                        label="sources"
                        inline={2}
                        summarizeAt={4}
                        summaryKind="cidr"
                        getItemText={(cidr) => cidr}
                    />;
            },
        },
        {
            key: 'dsts',
            header: 'Destinations',
            gridTrack: `${COLUMN_WIDTHS.dsts}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.dstCidrs.length === 0
                    ? <span className="yn-cell-mono yn-cell-muted">—</span>
                    : <ChipList
                        items={expanded.dstCidrs}
                        renderChip={(cidr, idx) => <IpNetChip key={idx} cidr={cidr} />}
                        label="destinations"
                        inline={2}
                        summarizeAt={4}
                        summaryKind="cidr"
                        getItemText={(cidr) => cidr}
                    />;
            },
        },
        {
            key: 'src_ports',
            header: 'Src ports',
            gridTrack: `${COLUMN_WIDTHS.src_ports}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.isAnySrcPort
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.srcPortRanges}
                        renderChip={(r, idx) => <PortRangeChip key={idx} rangeStr={r} />}
                        label="port ranges"
                        inline={2}
                        summarizeAt={4}
                    />;
            },
        },
        {
            key: 'dst_ports',
            header: 'Dst ports',
            gridTrack: `${COLUMN_WIDTHS.dst_ports}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.isAnyDstPort
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.dstPortRanges}
                        renderChip={(r, idx) => <PortRangeChip key={idx} rangeStr={r} />}
                        label="port ranges"
                        inline={2}
                        summarizeAt={4}
                    />;
            },
        },
        {
            key: 'protos',
            header: 'Protocols',
            gridTrack: `${COLUMN_WIDTHS.protos}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.protoRanges.length === 0
                    ? <span className="yn-cell-mono yn-cell-muted">—</span>
                    : <ChipList
                        items={expanded.protoRanges}
                        isAny={expanded.isAnyProto}
                        anyLabel="any"
                        renderChip={(r, idx) => <ProtoChip key={idx} rangeStr={r} />}
                        label="protocols"
                        inline={2}
                        summarizeAt={4}
                    />;
            },
        },
        {
            key: 'vlans',
            header: 'VLANs',
            gridTrack: `${COLUMN_WIDTHS.vlans}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.isAnyVlan
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.vlanRanges}
                        renderChip={(r, idx) => <VlanRangeChip key={idx} rangeStr={r} />}
                        label="VLAN ranges"
                        inline={2}
                        summarizeAt={4}
                    />;
            },
        },
        {
            key: 'devices',
            header: 'Devices',
            gridTrack: `${COLUMN_WIDTHS.devices}px`,
            renderCell: (item) => {
                const expanded = expandRuleItem(item.rule);
                return expanded.deviceNames.length === 0
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.deviceNames}
                        renderChip={(d, idx) => (
                            <span key={idx} className="acl-chip acl-chip--device" title={d}>{d}</span>
                        )}
                        label="devices"
                        inline={1}
                        summarizeAt={3}
                    />;
            },
        },
        {
            key: 'counter',
            header: 'Counter',
            gridTrack: `${COLUMN_WIDTHS.counter}px`,
            renderCell: (item) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }} title={item.counter || `rule ${item.index} (default)`}>
                    {item.counter
                        ? <span className="yn-cell-mono">{item.counter}</span>
                        : <span className="yn-cell-mono yn-cell-muted">rule {item.index}</span>
                    }
                </div>
            ),
        },
        {
            key: 'sparkline',
            header: 'pps',
            gridTrack: `${COLUMN_WIDTHS.sparkline}px`,
            renderCell: (item) => (
                <SparklineCell
                    item={item}
                    rate={rates.get(item.id)}
                    counterEnabled={enabledCounterNames.has(effectiveCounterName(item.rule, item.index))}
                    onToggleCounter={onToggleCounter}
                />
            ),
        },
        {
            key: 'actions',
            header: 'Actions',
            gridTrack: `${COLUMN_WIDTHS.actions}px`,
            renderCell: (item) => <ActionChain actions={item.rule.actions ?? []} />,
        },
    ], [rates, enabledCounterNames, onToggleCounter]);

    return (
        <VirtualTable<RuleItem>
            rows={items}
            columns={columns}
            getRowId={(item) => item.id}
            minWidth={MIN_WIDTH}
            columnGap={0}
            indexWidth={48}
            cellPaddingRight={8}
            indexFontSize={12}
            {...ruleTableCommonProps({
                items,
                onEditRule,
                selectedIds,
                onSelectionChange,
                activeRowId,
                flashRowId,
                currentIsDirty,
                onSave,
                onDiscard,
                onDeleteConfig,
            })}
            scrollActiveIntoView
            renderIndexAdornment={(item) => {
                const expanded = expandRuleItem(item.rule);
                if (expanded.isDead) {
                    return (
                        <span
                            className="acl-rule-badge acl-rule-badge--dead"
                            title={deadReasonText(expanded)}
                        >
                            dead
                        </span>
                    );
                }
                if (expanded.isL2) {
                    return (
                        <span
                            className="acl-rule-badge acl-rule-badge--l2"
                            title="No IP filter — matches L2 frames per VLAN/device"
                        >
                            L2
                        </span>
                    );
                }
                return null;
            }}
        />
    );
};

export default RuleTable;
