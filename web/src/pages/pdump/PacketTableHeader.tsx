import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { SortableHeader } from './SortableHeader';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';
import type { PacketSortState, PacketSortColumn } from './types';

export interface PacketTableHeaderProps {
    sortState: PacketSortState;
    onSort: (column: PacketSortColumn) => void;
}

export const PacketTableHeader: React.FC<PacketTableHeaderProps> = ({
    sortState,
    onSort,
}) => {
    return (
        <Box
            style={{
                display: 'flex',
                alignItems: 'center',
                height: HEADER_HEIGHT,
                minWidth: TOTAL_WIDTH,
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: 'var(--g-color-base-generic)',
                padding: '0 8px',
                flexShrink: 0,
            }}
        >
            <Box style={{ ...cellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableHeader
                column="time"
                label="Time"
                style={cellStyles.time}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableHeader
                column="source"
                label="Source"
                style={cellStyles.source}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableHeader
                column="destination"
                label="Destination"
                style={cellStyles.destination}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableHeader
                column="protocol"
                label="Protocol"
                style={cellStyles.protocol}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableHeader
                column="length"
                label="Length"
                style={{ ...cellStyles.length, textAlign: undefined }}
                sortState={sortState}
                onSort={onSort}
            />
            <Box style={{ ...cellStyles.info }}>
                <Text variant="subheader-1">Info</Text>
            </Box>
        </Box>
    );
};
