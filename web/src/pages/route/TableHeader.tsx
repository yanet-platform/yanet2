import React from 'react';
import { Box, Text, Checkbox } from '@gravity-ui/uikit';
import { SortableTableHeader } from '../../components';
import type { SortState, SortableColumn } from './types';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';
import './route.scss';

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
            <SortableTableHeader column="prefix" label="Prefix" style={cellStyles.prefix} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="next_hop" label="Next Hop" style={cellStyles.next_hop} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="peer" label="Peer" style={cellStyles.peer} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="is_best" label="Best" style={cellStyles.is_best} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="pref" label="Pref" style={cellStyles.pref} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="as_path_len" label="AS Path" style={cellStyles.as_path_len} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableTableHeader column="source" label="Source" style={cellStyles.source} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <Box style={cellStyles.actions}>
                <Text variant="subheader-1">Edit</Text>
            </Box>
        </Box>
    );
};
