import React from 'react';
import { Magnifier } from '@gravity-ui/icons';
import { Flex, Icon, TextInput } from '@gravity-ui/uikit';

interface SearchInputProps {
    value: string;
    onUpdate: (value: string) => void;
    placeholder?: string;
    controlRef?: React.RefObject<HTMLInputElement | null>;
}

export const SearchInput: React.FC<SearchInputProps> = ({
    value,
    onUpdate,
    placeholder,
    controlRef,
}) => {
    return (
        <TextInput
            controlRef={controlRef}
            value={value}
            onUpdate={onUpdate}
            placeholder={placeholder}
            startContent={
                <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                    <Icon data={Magnifier} size={16} />
                </Flex>
            }
            hasClear
            type="search"
        />
    );
};
