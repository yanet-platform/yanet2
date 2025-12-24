import React from 'react';
import { Box, Text, Checkbox } from '@gravity-ui/uikit';
import { HEADER_HEIGHT, TOTAL_WIDTH, cellStyles } from './constants';
import './forward.css';

export interface RuleTableHeaderProps {
    isAllSelected: boolean;
    isIndeterminate: boolean;
    onSelectAll: () => void;
    hasItems: boolean;
}

export const RuleTableHeader: React.FC<RuleTableHeaderProps> = ({
    isAllSelected,
    isIndeterminate,
    onSelectAll,
    hasItems,
}) => {
    return (
        <Box
            className="forward-table-header"
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

            {/* Target column header */}
            <Box style={cellStyles.target}>
                <Text variant="subheader-1">Target</Text>
            </Box>

            {/* Mode column header */}
            <Box style={cellStyles.mode}>
                <Text variant="subheader-1">Mode</Text>
            </Box>

            {/* Counter column header */}
            <Box style={cellStyles.counter}>
                <Text variant="subheader-1">Counter</Text>
            </Box>

            {/* Devices column header */}
            <Box style={cellStyles.devices}>
                <Text variant="subheader-1">Devices</Text>
            </Box>

            {/* VLANs column header */}
            <Box style={cellStyles.vlans}>
                <Text variant="subheader-1">VLANs</Text>
            </Box>

            {/* Sources column header */}
            <Box style={cellStyles.srcs}>
                <Text variant="subheader-1">Sources</Text>
            </Box>

            {/* Destinations column header */}
            <Box style={cellStyles.dsts}>
                <Text variant="subheader-1">Destinations</Text>
            </Box>

            {/* Actions column header */}
            <Box style={cellStyles.actions}>
                <Text variant="subheader-1">Edit</Text>
            </Box>
        </Box>
    );
};
