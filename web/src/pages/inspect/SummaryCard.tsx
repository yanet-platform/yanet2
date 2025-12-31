import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import type { IconData } from '@gravity-ui/uikit';
import './inspect.scss';

export interface SummaryCardProps {
    icon: IconData;
    label: string;
    value: number;
    color?: 'default' | 'info' | 'positive' | 'warning';
}

export const SummaryCard: React.FC<SummaryCardProps> = ({
    icon,
    label,
    value,
    color = 'default',
}) => {
    return (
        <Box className={`summary-card summary-card--${color}`}>
            <Box className="summary-card__icon">
                <Icon data={icon} size={20} />
            </Box>
            <Box className="summary-card__content">
                <Text variant="header-2" className="summary-card__value">
                    {value}
                </Text>
                <Text variant="body-1" color="secondary" className="summary-card__label">
                    {label}
                </Text>
            </Box>
        </Box>
    );
};
