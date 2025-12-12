import React from 'react';
import { Box, Text, Icon, Checkbox } from '@gravity-ui/uikit';
import { ChevronUp, ChevronDown } from '@gravity-ui/icons';
import { HEADER_HEIGHT, TOTAL_WIDTH, cellStyles } from './constants';

export interface PrefixTableHeaderProps {
    sortDirection: 'asc' | 'desc';
    onSort: () => void;
    isAllSelected: boolean;
    isIndeterminate: boolean;
    onSelectAll: () => void;
    hasItems: boolean;
}

export const PrefixTableHeader: React.FC<PrefixTableHeaderProps> = ({
    sortDirection,
    onSort,
    isAllSelected,
    isIndeterminate,
    onSelectAll,
    hasItems,
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
            {/* Checkbox column header */}
            <Box style={cellStyles.checkbox}>
                <Checkbox
                    checked={isAllSelected}
                    indeterminate={isIndeterminate}
                    onUpdate={onSelectAll}
                    disabled={!hasItems}
                    size="m"
                />
            </Box>

            {/* Index column header */}
            <Box style={{ ...cellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>

            {/* Prefix column header - sortable */}
            <Box
                style={{
                    ...cellStyles.prefix,
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 4,
                }}
                onClick={onSort}
            >
                <Text variant="subheader-1">Prefix</Text>
                <Icon data={sortDirection === 'asc' ? ChevronUp : ChevronDown} size={14} />
            </Box>
        </Box>
    );
};
