import React, { memo } from 'react';
import { Box, Checkbox, Text } from '@gravity-ui/uikit';
import { ROW_HEIGHT, TOTAL_WIDTH, cellStyles } from './constants';
import type { PrefixItem } from './types';

export interface PrefixRowProps {
    prefix: PrefixItem;
    index: number;
    start: number;
    isSelected: boolean;
    onSelect: (prefix: PrefixItem, checked: boolean) => void;
}

export const PrefixRow: React.FC<PrefixRowProps> = memo(({
    prefix,
    index,
    start,
    isSelected,
    onSelect,
}) => {
    const handleCheckboxUpdate = (checked: boolean) => {
        onSelect(prefix, checked);
    };

    return (
        <Box
            style={{
                position: 'absolute',
                top: start,
                left: 0,
                height: ROW_HEIGHT,
                minWidth: TOTAL_WIDTH,
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: isSelected ? 'var(--g-color-base-selection)' : 'transparent',
                paddingLeft: 8,
                paddingRight: 8,
            }}
        >
            {/* Checkbox */}
            <Box style={cellStyles.checkbox}>
                <Checkbox
                    checked={isSelected}
                    onUpdate={handleCheckboxUpdate}
                    size="m"
                />
            </Box>

            {/* Index */}
            <Box style={cellStyles.index}>
                <Text variant="body-1" color="secondary">
                    {(index + 1).toLocaleString()}
                </Text>
            </Box>

            {/* Prefix */}
            <Box style={cellStyles.prefix}>
                <Text variant="body-1">{prefix.prefix}</Text>
            </Box>
        </Box>
    );
});

PrefixRow.displayName = 'PrefixRow';

