import React from 'react';
import { Box, Text, Icon, Checkbox } from '@gravity-ui/uikit';
import { ChevronUp, ChevronDown } from '@gravity-ui/icons';
import { HEADER_HEIGHT, TOTAL_WIDTH, cellStyles } from './constants';
import './decap.css';

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
            className="prefix-table-header-box"
            style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
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
                className="prefix-table-header__sortable-cell"
                style={cellStyles.prefix}
                onClick={onSort}
            >
                <Text variant="subheader-1">Prefix</Text>
                <Icon data={sortDirection === 'asc' ? ChevronUp : ChevronDown} size={14} />
            </Box>
        </Box>
    );
};
