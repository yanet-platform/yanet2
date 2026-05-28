import React from 'react';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { SearchInput } from '../../../components';

interface DraftPageToolbarProps {
    title: string;
    searchValue: string;
    onSearchChange: (v: string) => void;
    searchPlaceholder: string;
    /** Optional slot for YAML import/export control. */
    yamlSlot?: React.ReactNode;
    addLabel: string;
    onAdd: () => void;
}

/** Page header toolbar shared by draft-style module pages (Route, Decap, …). */
const DraftPageToolbar: React.FC<DraftPageToolbarProps> = ({
    title,
    searchValue,
    onSearchChange,
    searchPlaceholder,
    yamlSlot,
    addLabel,
    onAdd,
}) => (
    <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
        <Text variant="header-1">{title}</Text>
        <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <SearchInput
                    value={searchValue}
                    onUpdate={onSearchChange}
                    placeholder={searchPlaceholder}
                />
            </div>
        {yamlSlot}
        <Button view="action" onClick={onAdd}>
            <Icon data={Plus} size={16} />
            {addLabel}
        </Button>
    </Flex>
);

export default DraftPageToolbar;
