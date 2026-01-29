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
import './VirtualizedAclTable.css';

// Format multiple IPNets for display
const formatIPNets = (nets: Rule['srcs']): string => {
    if (!nets || nets.length === 0) return '*';
    return nets.map(formatIPNet).join(', ');
};

// Format port ranges for display
const formatPortRanges = (ranges: Rule['src_port_ranges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatPortRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format proto ranges for display
const formatProtoRanges = (ranges: Rule['proto_ranges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatProtoRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format vlan ranges for display
const formatVlanRanges = (ranges: Rule['vlan_ranges']): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => formatVlanRange({ from: r.from || 0, to: r.to || 0 })).join(', ');
};

// Format devices for display
const formatDevices = (devices: Rule['devices']): string => {
    if (!devices || devices.length === 0) return '*';
    return devices.map((d) => d.name || '').filter(Boolean).join(', ') || '*';
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
        if (rule.action?.counter?.toLowerCase().includes(lowerQuery)) return true;

        // Search in action
        const actionStr = ACTION_LABELS[rule.action?.kind ?? 0].toLowerCase();
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
    const actionKind = rule.action?.kind ?? 0;
    const actionLabel = ACTION_LABELS[actionKind];
    const isPassAction = actionKind === 0;

    return (
        <div
            className={`acl-table__row ${index % 2 === 0 ? 'acl-table__row--even' : 'acl-table__row--odd'}`}
            style={{
                minWidth: TOTAL_WIDTH,
                height: ROW_HEIGHT,
                transform: `translateY(${start}px)`,
            }}
        >
            <div style={cellStyles.index}>{index + 1}</div>
            <div style={cellStyles.srcs} title={formatIPNets(rule.srcs)}>
                {formatIPNets(rule.srcs)}
            </div>
            <div style={cellStyles.dsts} title={formatIPNets(rule.dsts)}>
                {formatIPNets(rule.dsts)}
            </div>
            <div style={cellStyles.srcPorts} title={formatPortRanges(rule.src_port_ranges)}>
                {formatPortRanges(rule.src_port_ranges)}
            </div>
            <div style={cellStyles.dstPorts} title={formatPortRanges(rule.dst_port_ranges)}>
                {formatPortRanges(rule.dst_port_ranges)}
            </div>
            <div style={cellStyles.protocols} title={formatProtoRanges(rule.proto_ranges)}>
                {formatProtoRanges(rule.proto_ranges)}
            </div>
            <div style={cellStyles.vlans} title={formatVlanRanges(rule.vlan_ranges)}>
                {formatVlanRanges(rule.vlan_ranges)}
            </div>
            <div style={cellStyles.devices} title={formatDevices(rule.devices)}>
                {formatDevices(rule.devices)}
            </div>
            <div style={cellStyles.counter} title={rule.action?.counter || ''}>
                {rule.action?.counter || '-'}
            </div>
            <div style={cellStyles.action}>
                <Text
                    variant="body-2"
                    color={isPassAction ? 'positive' : 'danger'}
                    className="acl-table__action-text"
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
        className="acl-table__header"
        style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
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
        return <div ref={containerRef} className="acl-table__container" />;
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
        <div ref={containerRef} className="acl-table" style={{ height: containerHeight }}>
            {/* Search bar */}
            <Box className="acl-table__search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
                <Box className="acl-table__search-input">
                    <TextInput
                        placeholder="Search by source, destination, device..."
                        value={searchQuery}
                        onUpdate={onSearchChange}
                        size="m"
                        hasClear
                    />
                </Box>
                <Box className="acl-table__stats">
                    <Label theme="info" size="m">{statsText}</Label>
                </Box>
            </Box>

            {/* Table container */}
            <Box className="acl-table__wrapper">
                {/* Loading overlay */}
                {isLoading && (
                    <Box className="acl-table__loading-overlay">
                        <Loader size="l" />
                    </Box>
                )}
                <TableHeader />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="acl-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {filteredRules.length === 0 ? (
                        <Box className="acl-table__empty">
                            <EmptyState message={searchQuery.trim() ? 'No rules match your search' : 'No rules defined'} />
                        </Box>
                    ) : (
                        <div
                            className="acl-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
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
            <Box className="acl-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
