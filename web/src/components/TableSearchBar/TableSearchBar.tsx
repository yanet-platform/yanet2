import React from 'react';
import { Box, TextInput, Loader, Label, Text } from '@gravity-ui/uikit';
import './TableSearchBar.css';

export interface TableSearchBarProps {
    searchQuery: string;
    onSearchChange: (value: string) => void;
    isSearching: boolean;
    statsText: string;
    selectedText?: string | null;
    onClearSelection?: () => void;
    helperText?: string;
    placeholder?: string;
    height?: number;
    inputWidth?: number;
    className?: string;
}

export const TableSearchBar: React.FC<TableSearchBarProps> = ({
    searchQuery,
    onSearchChange,
    isSearching,
    statsText,
    selectedText,
    onClearSelection,
    helperText,
    placeholder = 'Search...',
    height = 48,
    inputWidth = 300,
    className = '',
}) => {
    return (
        <Box className={`table-search-bar ${className}`} style={{ height }}>
            <Box className="table-search-bar__input" style={{ width: inputWidth }}>
                <TextInput
                    placeholder={placeholder}
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box className="table-search-bar__stats">
                {isSearching && <Loader size="s" />}
                <Label theme="info" size="m">{statsText}</Label>
                {selectedText && (
                    <>
                        <Label theme="warning" size="m">{selectedText}</Label>
                        {onClearSelection && (
                            <Text
                                variant="body-1"
                                color="link"
                                className="table-search-bar__clear"
                                onClick={onClearSelection}
                            >
                                Clear
                            </Text>
                        )}
                    </>
                )}
                {helperText && (
                    <Text variant="body-2" color="secondary">
                        {helperText}
                    </Text>
                )}
            </Box>
        </Box>
    );
};
