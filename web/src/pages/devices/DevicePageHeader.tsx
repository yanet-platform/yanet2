import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageHeader } from '../../components';

export interface DevicePageHeaderProps {
    onCreateDevice: () => void;
}

export const DevicePageHeader: React.FC<DevicePageHeaderProps> = ({ onCreateDevice }) => {
    return (
        <PageHeader
            title="Devices"
            actions={
                <Button view="action" onClick={onCreateDevice}>
                    <Button.Icon>
                        <Plus />
                    </Button.Icon>
                    Create Device
                </Button>
            }
        />
    );
};
