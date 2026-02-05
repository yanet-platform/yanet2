import React, { memo } from 'react';
import { Box, Checkbox, Text, Button } from '@gravity-ui/uikit';
import { Pencil } from '@gravity-ui/icons';
import { ROW_HEIGHT, TOTAL_WIDTH, cellStyles } from './constants';
import { formatDevices, formatVlanRanges, formatIPNets, formatMode } from './hooks';
import type { RuleItem } from './types';

export interface RuleRowProps {
    ruleItem: RuleItem;
    start: number;
    isSelected: boolean;
    onSelect: (ruleItem: RuleItem, checked: boolean) => void;
    onEdit: (ruleItem: RuleItem) => void;
}

export const RuleRow: React.FC<RuleRowProps> = memo(({
    ruleItem,
    start,
    isSelected,
    onSelect,
    onEdit,
}) => {
    const { rule, index } = ruleItem;
    const action = rule.action;

    const handleCheckboxUpdate = (checked: boolean) => {
        onSelect(ruleItem, checked);
    };

    const handleEditClick = () => {
        onEdit(ruleItem);
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

            {/* Target */}
            <Box style={cellStyles.target} title={action?.target || ''}>
                <Text variant="body-1">{action?.target || '-'}</Text>
            </Box>

            {/* Mode */}
            <Box style={cellStyles.mode}>
                <Text variant="body-1">{formatMode(action?.mode)}</Text>
            </Box>

            {/* Counter */}
            <Box style={cellStyles.counter} title={action?.counter || ''}>
                <Text variant="body-1">{action?.counter || '-'}</Text>
            </Box>

            {/* Devices */}
            <Box style={cellStyles.devices} title={formatDevices(rule.devices)}>
                <Text variant="body-1">{formatDevices(rule.devices)}</Text>
            </Box>

            {/* VLANs */}
            <Box style={cellStyles.vlans} title={formatVlanRanges(rule.vlan_ranges)}>
                <Text variant="body-1">{formatVlanRanges(rule.vlan_ranges)}</Text>
            </Box>

            {/* Sources */}
            <Box style={cellStyles.srcs} title={formatIPNets(rule.srcs)}>
                <Text variant="body-1">{formatIPNets(rule.srcs)}</Text>
            </Box>

            {/* Destinations */}
            <Box style={cellStyles.dsts} title={formatIPNets(rule.dsts)}>
                <Text variant="body-1">{formatIPNets(rule.dsts)}</Text>
            </Box>

            {/* Actions */}
            <Box style={cellStyles.actions}>
                <Button
                    view="flat"
                    size="s"
                    onClick={handleEditClick}
                >
                    <Pencil />
                </Button>
            </Box>
        </Box>
    );
});

RuleRow.displayName = 'RuleRow';
