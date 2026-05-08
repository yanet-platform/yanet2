import React from 'react';
import { Box, Text, Icon } from '@gravity-ui/uikit';
import { PlugConnection, Layers } from '@gravity-ui/icons';
import type { LocalDevice } from './types';
import './devices.scss';

export interface DeviceListItemProps {
    device: LocalDevice;
    isSelected: boolean;
    onClick: () => void;
}

export const DeviceListItem: React.FC<DeviceListItemProps> = ({
    device,
    isSelected,
    onClick,
}) => {
    const className = [
        'device-list-item',
        isSelected && 'device-list-item--selected',
    ].filter(Boolean).join(' ');

    const isVlan = device.type === 'vlan';
    const DeviceIcon = isVlan ? Layers : PlugConnection;
    const iconClassName = `device-list-item__icon device-list-item__icon--${isVlan ? 'vlan' : 'port'}`;

    return (
        <Box className={className} onClick={onClick}>
            <Icon data={DeviceIcon} size={16} className={iconClassName} />
            <Text variant="body-1" ellipsis>
                {device.id.name}
            </Text>
            {device.isDirty && (
                <Box className="device-list-item__dirty-indicator" />
            )}
        </Box>
    );
};
