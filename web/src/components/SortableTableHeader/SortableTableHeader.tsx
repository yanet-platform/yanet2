import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { ChevronUp, ChevronDown } from '@gravity-ui/icons';

export interface SortState<TColumn = string> {
    column: TColumn | null;
    direction: 'asc' | 'desc';
}

export interface SortableTableHeaderProps<TColumn = string> {
    column: TColumn;
    label: string;
    style: React.CSSProperties;
    sortState: SortState<TColumn>;
    onSort: (column: TColumn) => void;
    disabled?: boolean;
}

export const SortableTableHeader = <TColumn extends string = string>({
    column,
    label,
    style,
    sortState,
    onSort,
    disabled = false,
}: SortableTableHeaderProps<TColumn>) => {
    const handleClick = () => {
        if (!disabled) {
            onSort(column);
        }
    };

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
};
