import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { SortableTableHeader } from '../../components';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';
import type { PacketSortState, PacketSortColumn } from './types';
import './pdump.scss';

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
            className="packet-table-header"
            style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
        >
            <Box style={cellStyles.index}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableTableHeader
                column="time"
                label="Time"
                style={cellStyles.time}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="source"
                label="Source"
                style={cellStyles.source}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="destination"
                label="Destination"
                style={cellStyles.destination}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="protocol"
                label="Protocol"
                style={cellStyles.protocol}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="length"
                label="Length"
                style={cellStyles.length}
                sortState={sortState}
                onSort={onSort}
            />
            <Box style={cellStyles.info}>
                <Text variant="subheader-1">Info</Text>
            </Box>
        </Box>
    );
};
