import React, { useEffect, useMemo, useRef } from 'react';
import { Magnifier } from '@gravity-ui/icons';
import { Flex, Icon, TextInput } from '@gravity-ui/uikit';

interface SearchInputProps {
    value: string;
    onUpdate: (value: string) => void;
    placeholder?: string;
    controlRef?: React.RefObject<HTMLInputElement | null>;
    enableFocusShortcut?: boolean;
    showShortcutHint?: boolean;
}

const isMacPlatform = (): boolean => {
    if (typeof navigator === 'undefined') {
        return false;
    }
    const platform = navigator.platform || '';
    const userAgent = navigator.userAgent || '';
    return /Mac|iPhone|iPad|iPod/.test(platform) || /Macintosh|Mac OS X/.test(userAgent);
};

const isSearchShortcut = (e: KeyboardEvent): boolean => {
    const key = e.key.toLowerCase();
    if (key !== 'k') {
        return false;
    }

    const isMac = isMacPlatform();
    if (isMac) {
        return e.metaKey && !e.ctrlKey;
    }

    return e.ctrlKey && !e.metaKey;
};

const appendSearchShortcutHint = (placeholder = 'Search', showShortcutHint = true): string => {
    if (!showShortcutHint) {
        return placeholder;
    }

    const isMac = isMacPlatform();
    const shortcut = isMac ? '⌘K' : 'Ctrl+K';
    const hasHint = placeholder.includes('(⌘K)')
        || placeholder.includes('(Ctrl+K)')
        || placeholder.includes('(/)');
    if (hasHint) {
        return placeholder;
    }

    return `${placeholder} (${shortcut})`;
};

export const SearchInput: React.FC<SearchInputProps> = ({
    value,
    onUpdate,
    placeholder,
    controlRef,
    enableFocusShortcut = true,
    showShortcutHint = true,
}) => {
    const fallbackRef = useRef<HTMLInputElement | null>(null);
    const inputRef = controlRef ?? fallbackRef;

    useEffect(() => {
        if (!enableFocusShortcut) {
            return;
        }

        const onKeyDown = (e: KeyboardEvent): void => {
            if (isSearchShortcut(e)) {
                e.preventDefault();
                inputRef.current?.focus();
            }
        };

        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, [enableFocusShortcut, inputRef]);

    const placeholderWithHint = useMemo(() => {
        if (placeholder === undefined) {
            return placeholder;
        }

        return appendSearchShortcutHint(placeholder, showShortcutHint);
    }, [placeholder, showShortcutHint]);

    return (
        <TextInput
            controlRef={inputRef}
            value={value}
            onUpdate={onUpdate}
            placeholder={placeholderWithHint}
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
