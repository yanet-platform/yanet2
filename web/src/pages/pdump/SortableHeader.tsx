import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ChevronUp, ChevronDown } from '@gravity-ui/icons';
import type { PacketSortState, PacketSortColumn } from './types';

export interface SortableHeaderProps {
    column: PacketSortColumn;
    label: string;
    style: React.CSSProperties;
    sortState: PacketSortState;
    onSort: (column: PacketSortColumn) => void;
}

export const SortableHeader: React.FC<SortableHeaderProps> = ({
    column,
    label,
    style,
    sortState,
    onSort,
}) => {
    const handleClick = () => {
        onSort(column);
    };

    const isActive = sortState.column === column;

    return (
        <Box
            style={{
                ...style,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: 4,
            }}
            onClick={handleClick}
        >
            <Text variant="subheader-1">{label}</Text>
            {isActive && (
                <Icon data={sortState.direction === 'asc' ? ChevronUp : ChevronDown} size={14} />
            )}
        </Box>
    );
};

