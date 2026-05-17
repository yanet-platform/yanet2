import React from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';

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
                <TextInput
                    controlRef={searchRef}
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    placeholder="Search devices… (⌘K)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            <Button view="action" onClick={onCreateDevice}>
                <Icon data={Plus} size={16} />
                Create Device
            </Button>
        </Flex>
    );
};
