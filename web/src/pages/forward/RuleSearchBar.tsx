import React from 'react';
import { Box, TextInput, Loader, Label, Text } from '@gravity-ui/uikit';
import { SEARCH_BAR_HEIGHT } from './constants';
import './forward.css';

export interface RuleSearchBarProps {
    searchQuery: string;
    onSearchChange: (value: string) => void;
    isSearching: boolean;
    statsText: string;
    selectedText: string | null;
    onClearSelection?: () => void;
}

export const RuleSearchBar: React.FC<RuleSearchBarProps> = ({
    searchQuery,
    onSearchChange,
    isSearching,
    statsText,
    selectedText,
    onClearSelection,
}) => {
    return (
        <Box className="forward-search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
            <Box className="forward-search-bar__input">
                <TextInput
                    placeholder="Search by target, counter, devices..."
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box className="forward-search-bar__stats">
                {isSearching && <Loader size="s" />}
                <Label theme="info" size="m">{statsText}</Label>
                {selectedText && (
                    <>
                        <Label theme="warning" size="m">{selectedText}</Label>
                        {onClearSelection && (
                            <Text
                                variant="body-1"
                                color="link"
                                className="forward-search-bar__clear"
                                onClick={onClearSelection}
                            >
                                Clear
                            </Text>
                        )}
                    </>
                )}
            </Box>
            <Box className="forward-search-bar__spacer" />
        </Box>
    );
};
