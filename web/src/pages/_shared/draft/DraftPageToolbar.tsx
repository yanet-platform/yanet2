import React from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';

interface DraftPageToolbarProps {
    title: string;
    searchValue: string;
    onSearchChange: (v: string) => void;
    searchPlaceholder: string;
    searchRef?: React.RefObject<HTMLInputElement | null>;
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
    searchRef,
    yamlSlot,
    addLabel,
    onAdd,
}) => (
    <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
        <Text variant="header-1">{title}</Text>
        <Flex grow />
        <div style={{ flexBasis: 380, flexShrink: 1 }}>
            <TextInput
                controlRef={searchRef}
                value={searchValue}
                onUpdate={onSearchChange}
                placeholder={searchPlaceholder}
                startContent={
                    <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                        <Icon data={Magnifier} size={16} />
                    </Flex>
                }
                hasClear
                type="search"
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
