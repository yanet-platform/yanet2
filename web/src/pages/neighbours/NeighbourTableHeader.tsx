import React from 'react';
import { Box, Text, Checkbox } from '@gravity-ui/uikit';
import { SortableHeader } from './SortableHeader';
import type { SortState, SortableColumn } from './hooks';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';

export interface NeighbourTableHeaderProps {
    sortState: SortState;
    onSort: (column: SortableColumn) => void;
}

export const NeighbourTableHeader: React.FC<NeighbourTableHeaderProps> = ({
    sortState,
    onSort,
}) => {
    return (
        <Box
            className="neigh-table-header"
            style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
        >
            <Box style={cellStyles.checkbox}>
                <Checkbox checked={false} disabled />
            </Box>
            <Box style={{ ...cellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableHeader column="next_hop" label="Next Hop" style={cellStyles.next_hop} sortState={sortState} onSort={onSort} />
            <SortableHeader column="link_addr" label="Neighbour MAC" style={cellStyles.link_addr} sortState={sortState} onSort={onSort} />
            <SortableHeader column="hardware_addr" label="Interface MAC" style={cellStyles.hardware_addr} sortState={sortState} onSort={onSort} />
            <SortableHeader column="device" label="Device" style={cellStyles.device} sortState={sortState} onSort={onSort} />
            <SortableHeader column="state" label="State" style={cellStyles.state} sortState={sortState} onSort={onSort} />
            <SortableHeader column="source" label="Source" style={cellStyles.source} sortState={sortState} onSort={onSort} />
            <SortableHeader column="priority" label="Priority" style={cellStyles.priority} sortState={sortState} onSort={onSort} />
            <SortableHeader column="updated_at" label="Updated At" style={cellStyles.updated_at} sortState={sortState} onSort={onSort} />
            <Box style={cellStyles.actions}>
                <Text variant="subheader-1">Edit</Text>
            </Box>
        </Box>
    );
};
