import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { DeviceListItem } from './DeviceListItem';
import type { LocalDevice } from './types';
import './devices.scss';

export interface DevicesListProps {
    devices: LocalDevice[];
    selectedDeviceName: string | null;
    onSelectDevice: (deviceName: string) => void;
}

export const DevicesList: React.FC<DevicesListProps> = ({
    devices,
    selectedDeviceName,
    onSelectDevice,
}) => {
    return (
        <Box className="devices-list">
            <Box className="devices-list__items">
                {devices.map((device) => (
                    <DeviceListItem
                        key={device.id.name}
                        device={device}
                        isSelected={device.id.name === selectedDeviceName}
                        onClick={() => onSelectDevice(device.id.name || '')}
                    />
                ))}
                {devices.length === 0 && (
                    <Box className="devices-list__empty">
                        <Text variant="body-1" color="secondary">
                            No devices
                        </Text>
                    </Box>
                )}
            </Box>
        </Box>
    );
};
