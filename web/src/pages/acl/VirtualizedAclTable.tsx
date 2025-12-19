import React, { useRef, useMemo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text, TextInput, Label, Loader } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { Rule } from '../../api/acl';
import type { AclTableProps } from './types';
import { useContainerHeight } from './hooks';
import {
    ROW_HEIGHT,
    HEADER_HEIGHT,
    SEARCH_BAR_HEIGHT,
    FOOTER_HEIGHT,
    OVERSCAN,
    TOTAL_WIDTH,
    cellStyles,
    ACTION_LABELS,
} from './constants';
import { formatIPNet, formatPortRange, formatProtoRange, formatVlanRange } from './yamlParser';

// Format multiple IPNets for display
const formatIPNets = (nets: Rule['srcs']): string => {
    if (!nets || nets.length === 0) return '*';
    return nets.map(formatIPNet).join(', ');
};

// Format port ranges for display
const formatPortRanges = (ranges: Rule['srcPortRanges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatPortRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format proto ranges for display
const formatProtoRanges = (ranges: Rule['protoRanges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatProtoRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format vlan ranges for display
const formatVlanRanges = (ranges: Rule['vlanRanges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatVlanRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format devices for display
const formatDevices = (devices: string[] | undefined): string => {
    if (!devices || devices.length === 0) return '*';
    return devices.join(', ');
};

// Filter rules by search query
const filterRules = (rules: Rule[], query: string): Rule[] => {
    if (!query.trim()) return rules;

    const lowerQuery = query.toLowerCase();
    return rules.filter((rule) => {
        // Search in sources
        const srcsStr = formatIPNets(rule.srcs).toLowerCase();
        if (srcsStr.includes(lowerQuery)) return true;

        // Search in destinations
        const dstsStr = formatIPNets(rule.dsts).toLowerCase();
        if (dstsStr.includes(lowerQuery)) return true;

        // Search in devices
        const devicesStr = formatDevices(rule.devices).toLowerCase();
        if (devicesStr.includes(lowerQuery)) return true;

        // Search in counter
        if (rule.counter?.toLowerCase().includes(lowerQuery)) return true;

        // Search in action
        const actionStr = ACTION_LABELS[rule.action ?? 0].toLowerCase();
        if (actionStr.includes(lowerQuery)) return true;

        return false;
    });
};

interface TableRowProps {
    rule: Rule;
    index: number;
    start: number;
}

const TableRow: React.FC<TableRowProps> = React.memo(({ rule, index, start }) => {
    const action = rule.action ?? 0;
    const actionLabel = ACTION_LABELS[action];
    const isPassAction = action === 0;

    return (
        <div
            style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                minWidth: TOTAL_WIDTH,
                height: ROW_HEIGHT,
                transform: `translateY(${start}px)`,
                display: 'flex',
                alignItems: 'center',
                padding: '0 8px',
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: index % 2 === 0
                    ? 'transparent'
                    : 'var(--g-color-base-generic-ultralight)',
                boxSizing: 'border-box',
            }}
        >
            <div style={cellStyles.index}>{index + 1}</div>
            <div style={cellStyles.srcs} title={formatIPNets(rule.srcs)}>
                {formatIPNets(rule.srcs)}
            </div>
            <div style={cellStyles.dsts} title={formatIPNets(rule.dsts)}>
                {formatIPNets(rule.dsts)}
            </div>
            <div style={cellStyles.srcPorts} title={formatPortRanges(rule.srcPortRanges)}>
                {formatPortRanges(rule.srcPortRanges)}
            </div>
            <div style={cellStyles.dstPorts} title={formatPortRanges(rule.dstPortRanges)}>
                {formatPortRanges(rule.dstPortRanges)}
            </div>
            <div style={cellStyles.protocols} title={formatProtoRanges(rule.protoRanges)}>
                {formatProtoRanges(rule.protoRanges)}
            </div>
            <div style={cellStyles.vlans} title={formatVlanRanges(rule.vlanRanges)}>
                {formatVlanRanges(rule.vlanRanges)}
            </div>
            <div style={cellStyles.devices} title={formatDevices(rule.devices)}>
                {formatDevices(rule.devices)}
            </div>
            <div style={cellStyles.counter} title={rule.counter || ''}>
                {rule.counter || '-'}
            </div>
            <div style={cellStyles.action}>
                <Text
                    variant="body-2"
                    color={isPassAction ? 'positive' : 'danger'}
                    style={{ fontWeight: 500 }}
                >
                    {actionLabel}
                </Text>
            </div>
        </div>
    );
});

TableRow.displayName = 'TableRow';

const TableHeader: React.FC = () => (
    <div
        style={{
            display: 'flex',
            alignItems: 'center',
            height: HEADER_HEIGHT,
            padding: '0 8px',
            borderBottom: '1px solid var(--g-color-line-generic)',
            backgroundColor: 'var(--g-color-base-generic)',
            fontWeight: 500,
            minWidth: TOTAL_WIDTH,
            boxSizing: 'border-box',
        }}
    >
        <div style={cellStyles.index}>#</div>
        <div style={cellStyles.srcs}>Sources</div>
        <div style={cellStyles.dsts}>Destinations</div>
        <div style={cellStyles.srcPorts}>Src Ports</div>
        <div style={cellStyles.dstPorts}>Dst Ports</div>
        <div style={cellStyles.protocols}>Protocols</div>
        <div style={cellStyles.vlans}>VLANs</div>
        <div style={cellStyles.devices}>Devices</div>
        <div style={cellStyles.counter}>Counter</div>
        <div style={cellStyles.action}>Action</div>
    </div>
);

export const VirtualizedAclTable: React.FC<AclTableProps> = ({
    rules,
    searchQuery,
    onSearchChange,
    isLoading = false,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    const containerHeight = useContainerHeight(containerRef);

    // Filter rules based on search
    const filteredRules = useMemo(() => filterRules(rules, searchQuery), [rules, searchQuery]);

    const rowVirtualizer = useVirtualizer({
        count: filteredRules.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} style={{ height: '100%' }} />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    const statsText = searchQuery.trim()
        ? `Found: ${filteredRules.length.toLocaleString()} of ${rules.length.toLocaleString()}`
        : `Total: ${rules.length.toLocaleString()}`;

    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${filteredRules.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} style={{ height: containerHeight, display: 'flex', flexDirection: 'column' }}>
            {/* Search bar */}
            <Box style={{ display: 'flex', alignItems: 'center', gap: 16, height: SEARCH_BAR_HEIGHT, flexShrink: 0 }}>
                <Box style={{ width: 350 }}>
                    <TextInput
                        placeholder="Search by source, destination, device..."
                        value={searchQuery}
                        onUpdate={onSearchChange}
                        size="m"
                        hasClear
                    />
                </Box>
                <Box style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Label theme="info" size="m">{statsText}</Label>
                </Box>
            </Box>

            {/* Table container */}
            <Box
                style={{
                    flex: 1,
                    border: '1px solid var(--g-color-line-generic)',
                    borderRadius: 8,
                    overflow: 'hidden',
                    display: 'flex',
                    flexDirection: 'column',
                    position: 'relative',
                }}
            >
                {/* Loading overlay */}
                {isLoading && (
                    <Box
                        style={{
                            position: 'absolute',
                            top: 0,
                            left: 0,
                            right: 0,
                            bottom: 0,
                            backgroundColor: 'var(--g-color-base-background)',
                            opacity: 0.7,
                            zIndex: 10,
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                        }}
                    >
                        <Loader size="l" />
                    </Box>
                )}
                <TableHeader />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    style={{
                        height: tableBodyHeight,
                        overflow: 'auto',
                        contain: 'strict',
                    }}
                >
                    {filteredRules.length === 0 ? (
                        <Box style={{ padding: '40px 20px', textAlign: 'center' }}>
                            <EmptyState message={searchQuery.trim() ? 'No rules match your search' : 'No rules defined'} />
                        </Box>
                    ) : (
                        <div
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                width: '100%',
                                minWidth: TOTAL_WIDTH,
                                position: 'relative',
                            }}
                        >
                            {virtualRows.map((virtualRow) => {
                                const rule = filteredRules[virtualRow.index];
                                if (!rule) return null;

                                return (
                                    <TableRow
                                        key={virtualRow.index}
                                        rule={rule}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            {/* Footer */}
            <Box style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', height: FOOTER_HEIGHT, flexShrink: 0 }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
