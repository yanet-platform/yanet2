import React from 'react';
import { Box, Text, Checkbox } from '@gravity-ui/uikit';
import { SortableHeader } from './SortableHeader';
import type { SortState, SortableColumn } from './types';
import { cellStyles, TOTAL_WIDTH, HEADER_HEIGHT } from './constants';

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
            <Box style={{ ...cellStyles.checkbox }}>
                <Checkbox checked={false} disabled />
            </Box>
            <Box style={{ ...cellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableHeader column="prefix" label="Prefix" style={cellStyles.prefix} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="nextHop" label="Next Hop" style={cellStyles.nextHop} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="peer" label="Peer" style={cellStyles.peer} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="isBest" label="Best" style={cellStyles.isBest} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="pref" label="Pref" style={cellStyles.pref} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="asPathLen" label="AS Path" style={cellStyles.asPathLen} sortState={sortState} onSort={onSort} disabled={!canSort} />
            <SortableHeader column="source" label="Source" style={cellStyles.source} sortState={sortState} onSort={onSort} disabled={!canSort} />
        </Box>
    );
};
