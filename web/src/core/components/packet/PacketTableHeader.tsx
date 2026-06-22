import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { SortableTableHeader } from '../SortableTableHeader';
import { pktCellStyles, PKT_TOTAL_WIDTH, PKT_HEADER_HEIGHT } from './constants';
import type { PacketSortState, PacketSortColumn } from './types';

export interface SharedPacketTableHeaderProps {
    sortState: PacketSortState;
    onSort: (column: PacketSortColumn) => void;
    /** Whether to render the Time column. Defaults to true. */
    showTime?: boolean;
}

export const SharedPacketTableHeader: React.FC<SharedPacketTableHeaderProps> = ({
    sortState,
    onSort,
    showTime = true,
}) => {
    return (
        <Box
            className="packet-table-header"
            style={{ height: PKT_HEADER_HEIGHT, minWidth: PKT_TOTAL_WIDTH }}
        >
            <Box style={pktCellStyles.index}>
                <Text variant="subheader-1">#</Text>
            </Box>
            {showTime && (
                <SortableTableHeader
                    column="time"
                    label="Time"
                    style={pktCellStyles.time}
                    sortState={sortState}
                    onSort={onSort}
                />
            )}
            <SortableTableHeader
                column="source"
                label="Source"
                style={pktCellStyles.source}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="destination"
                label="Destination"
                style={pktCellStyles.destination}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="protocol"
                label="Protocol"
                style={pktCellStyles.protocol}
                sortState={sortState}
                onSort={onSort}
            />
            <SortableTableHeader
                column="length"
                label="Length"
                style={pktCellStyles.length}
                sortState={sortState}
                onSort={onSort}
            />
            <Box style={pktCellStyles.info}>
                <Text variant="subheader-1">Info</Text>
            </Box>
        </Box>
    );
};
