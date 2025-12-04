import React, { memo, useCallback } from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ChevronUp, ChevronDown } from '@gravity-ui/icons';
import type { SortState, SortableColumn } from './types';

export interface SortableHeaderProps {
    column: SortableColumn;
    label: string;
    style: React.CSSProperties;
    sortState: SortState;
    onSort: (column: SortableColumn) => void;
    disabled?: boolean;
}

export const SortableHeader = memo(({ column, label, style, sortState, onSort, disabled }: SortableHeaderProps) => {
    const handleClick = useCallback(() => {
        if (!disabled) {
            onSort(column);
        }
    }, [column, onSort, disabled]);

    const isActive = sortState.column === column;

    return (
        <Box
            style={{
                ...style,
                cursor: disabled ? 'default' : 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: 4,
                opacity: disabled ? 0.6 : 1,
            }}
            onClick={handleClick}
        >
            <Text variant="subheader-1">{label}</Text>
            {isActive && (
                <Icon data={sortState.direction === 'asc' ? ChevronUp : ChevronDown} size={14} />
            )}
        </Box>
    );
});

SortableHeader.displayName = 'SortableHeader';
