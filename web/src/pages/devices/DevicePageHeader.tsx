import React from 'react';
import { Flex, Text, Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

export interface DevicePageHeaderProps {
    onCreateDevice: () => void;
}

export const DevicePageHeader: React.FC<DevicePageHeaderProps> = ({ onCreateDevice }) => {
    return (
        <Flex
            alignItems="center"
            justifyContent="space-between"
            style={{ width: '100%' }}
        >
            <Text variant="header-1">Devices</Text>
            <Button view="action" onClick={onCreateDevice}>
                <Button.Icon>
                    <Plus />
                </Button.Icon>
                Create Device
            </Button>
        </Flex>
    );
};
