import React from 'react';
import { Box, Text, Checkbox } from '@gravity-ui/uikit';
import { SortableHeader } from './SortableHeader';
import type { SortState, SortableColumn } from './types';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';
import './route.css';

export interface TableHeaderProps {
    sortState: SortState;
    onSort: (column: SortableColumn) => void;
    canSort: boolean;
}

export const TableHeader: React.FC<TableHeaderProps> = ({
    sortState,
    onSort,
    canSort,
}) => {
    return (
        <Box
            className="route-table-header-box"
            style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
        >
            <Box style={cellStyles.checkbox}>
                <Checkbox checked={false} disabled />
            </Box>
            <Box style={{ ...cellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableHeader column="prefix" label="Prefix" style={cellStyles.prefix} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="next_hop" label="Next Hop" style={cellStyles.next_hop} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="peer" label="Peer" style={cellStyles.peer} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="is_best" label="Best" style={cellStyles.is_best} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="pref" label="Pref" style={cellStyles.pref} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="as_path_len" label="AS Path" style={cellStyles.as_path_len} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="source" label="Source" style={cellStyles.source} sortState={sortState} onSort={onSort} disabled={!canSort} />
        </Box>
    );
};
