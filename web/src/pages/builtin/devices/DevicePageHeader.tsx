import React from 'react';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { SearchInput } from '../../../components';

export interface DevicePageHeaderProps {
    onCreateDevice: () => void;
    searchQuery: string;
    onSearchChange: (value: string) => void;
    searchRef: React.RefObject<HTMLInputElement | null>;
}

/** Page header rendered inside the PageLayout header slot. */
export const DevicePageHeader: React.FC<DevicePageHeaderProps> = ({
    onCreateDevice,
    searchQuery,
    onSearchChange,
    searchRef,
}) => {
    return (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Devices</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <SearchInput
                    controlRef={searchRef}
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    placeholder="Search devices… (⌘K)"
                />
            </div>
            <Button view="action" onClick={onCreateDevice}>
                <Icon data={Plus} size={16} />
                Create Device
            </Button>
        </Flex>
    );
};
