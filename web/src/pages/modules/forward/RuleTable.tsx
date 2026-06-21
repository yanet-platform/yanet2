import React from 'react';
import type { RuleItem } from './types';
import type { RuleRate } from './useForwardRuleCounters';
import DirectionBadge from './DirectionBadge';
import AnyBadge from './AnyBadge';
import Sparkline from '@yanet/core/components/Sparkline';
import { formatPps } from '@yanet/core/utils';
import { ruleTableCommonProps } from '@yanet/core/components/draft';
import { VirtualTable, type Column } from '@yanet/core/components/VirtualTable';

/** Compact mono list of values with overflow truncation. */
const ValueCell: React.FC<{ values: string[] }> = ({ values }) => {
    if (values.length === 0) return null;
    const visible = values.slice(0, 3);
    const rest = values.length - visible.length;
    return (
        <span className="yn-cell-values" title={values.join(', ')}>
            {visible.map((v, idx) => (
                <span key={idx} className="yn-cell-mono">{v}</span>
            ))}
            {rest > 0 && <span className="yn-cell-more">+{rest}</span>}
        </span>
    );
};

/**
 * Minimum grid content width: paddingLeft(16) + checkbox(38) + gap(14) + index(52) +
 * gap(14) + data-cols(1200) + 7×gap(98) + paddingRight(22) = 1454.
 */
const MIN_WIDTH = 1454;

interface RuleTableProps {
    items: RuleItem[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    /** Map from RuleItem.id to rate data (sparkline history + live pps). */
    rateValues: Map<string, RuleRate>;
    onSelectionChange: (ids: Set<string>) => void;
    onEditRule: (item: RuleItem) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
    /** Row id to scroll to and flash on mount / change. */
    flashRowId?: string | null;
}

/** Virtualized rule table — thin wrapper over the shared VirtualTable shell. */
const RuleTable: React.FC<RuleTableProps> = ({
    items,
    selectedIds,
    activeRowId,
    rateValues,
    onSelectionChange,
    onEditRule,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
    flashRowId,
}) => {
    const columns: Column<RuleItem>[] = [
        {
            key: 'target',
            header: 'Target',
            gridTrack: '160px',
            renderCell: (item) => (
                <span className="yn-cell-mono yn-cell-strong" title={item.target}>
                    {item.target || '—'}
                </span>
            ),
        },
        {
            key: 'mode',
            header: 'Mode',
            gridTrack: '90px',
            renderCell: (item) => <DirectionBadge mode={item.mode} />,
        },
        {
            key: 'counter',
            header: 'Counter',
            gridTrack: '140px',
            renderCell: (item) => (
                <span className="yn-cell-mono yn-cell-muted" title={item.counter}>
                    {item.counter || '—'}
                </span>
            ),
        },
        {
            key: 'devices',
            header: 'Devices',
            gridTrack: '140px',
            renderCell: (item) => item.deviceNames.length > 0
                ? <ValueCell values={item.deviceNames} />
                : <AnyBadge label="any" />,
        },
        {
            key: 'vlans',
            header: 'VLANs',
            gridTrack: '120px',
            renderCell: (item) => item.isAllVlans
                ? <AnyBadge label="any" />
                : <span className="yn-cell-mono yn-cell-muted">{item.vlansDisplay || '—'}</span>,
        },
        {
            key: 'srcs',
            header: 'Sources',
            gridTrack: '200px',
            renderCell: (item) => item.isAnySrc
                ? <AnyBadge label="any" />
                : <ValueCell values={item.sourceCidrs} />,
        },
        {
            key: 'dsts',
            header: 'Destinations',
            gridTrack: '200px',
            renderCell: (item) => item.isAnyDst
                ? <AnyBadge label="any" />
                : <ValueCell values={item.dstCidrs} />,
        },
        {
            key: 'pps',
            header: 'pps',
            gridTrack: '150px',
            renderCell: (item) => {
                const rateData = rateValues.get(item.id) ?? null;
                return (
                    <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Sparkline values={rateData?.history ?? null} width={56} height={16} emptyTitle="No counter history available from backend" />
                        <span className="yn-cell-pps">
                            {rateData ? formatPps(rateData.pps) : '— pps'}
                        </span>
                    </span>
                );
            },
        },
    ];

    return (
        <VirtualTable<RuleItem>
            rows={items}
            columns={columns}
            getRowId={(item) => item.id}
            minWidth={MIN_WIDTH}
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
        />
    );
};

export default RuleTable;
