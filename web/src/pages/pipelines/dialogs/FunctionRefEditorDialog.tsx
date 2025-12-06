import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { TextInput, Box, Text } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';
import type { FunctionRefNodeData } from '../types';
import type { FunctionId } from '../../../api/common';

export interface FunctionRefEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (data: FunctionRefNodeData) => void;
    initialData: FunctionRefNodeData;
    availableFunctions: FunctionId[];
    loadingFunctions?: boolean;
}

export const FunctionRefEditorDialog: React.FC<FunctionRefEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    initialData,
    availableFunctions,
    loadingFunctions = false,
}) => {
    const [functionName, setFunctionName] = useState('');
    const [showSuggestions, setShowSuggestions] = useState(false);
    
    useEffect(() => {
        if (open) {
            setFunctionName(initialData.functionName || '');
            setShowSuggestions(false);
        }
    }, [open, initialData]);
    
    const handleConfirm = useCallback(() => {
        onConfirm({
            functionName: functionName.trim(),
        });
    }, [functionName, onConfirm]);
    
    // Filter suggestions based on input
    const suggestions = useMemo(() => {
        if (!functionName.trim()) {
            return availableFunctions;
        }
        const lowerInput = functionName.toLowerCase();
        return availableFunctions.filter(f => 
            f.name?.toLowerCase().includes(lowerInput)
        );
    }, [functionName, availableFunctions]);
    
    const handleInputChange = useCallback((value: string) => {
        setFunctionName(value);
        setShowSuggestions(true);
    }, []);
    
    const handleSuggestionClick = useCallback((name: string) => {
        setFunctionName(name);
        setShowSuggestions(false);
    }, []);
    
    const handleInputFocus = useCallback(() => {
        setShowSuggestions(true);
    }, []);
    
    const handleInputBlur = useCallback(() => {
        // Delay hiding to allow click on suggestion
        setTimeout(() => setShowSuggestions(false), 200);
    }, []);
    
    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Edit Function Reference"
            confirmText="Save"
            showCancel={false}
        >
            <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
                <FormField
                    label="Function Name"
                    hint="Name of the function to reference. You can enter a name that doesn't exist yet."
                >
                    <div style={{ position: 'relative' }}>
                        <TextInput
                            value={functionName}
                            onUpdate={handleInputChange}
                            onFocus={handleInputFocus}
                            onBlur={handleInputBlur}
                            placeholder={loadingFunctions ? 'Loading functions...' : 'Enter function name'}
                            style={{ width: '100%' }}
                            autoFocus
                        />
                        {showSuggestions && suggestions.length > 0 && (
                            <Box
                                style={{
                                    position: 'absolute',
                                    top: '100%',
                                    left: 0,
                                    right: 0,
                                    maxHeight: '200px',
                                    overflowY: 'auto',
                                    background: 'var(--g-color-base-float)',
                                    border: '1px solid var(--g-color-line-generic)',
                                    borderRadius: '4px',
                                    marginTop: '4px',
                                    zIndex: 1000,
                                    boxShadow: '0 4px 8px rgba(0, 0, 0, 0.1)',
                                }}
                            >
                                {suggestions.map((func) => (
                                    <Box
                                        key={func.name}
                                        style={{
                                            padding: '8px 12px',
                                            cursor: 'pointer',
                                            borderBottom: '1px solid var(--g-color-line-generic)',
                                        }}
                                        onClick={() => handleSuggestionClick(func.name || '')}
                                        onMouseDown={(e) => e.preventDefault()}
                                    >
                                        <Text variant="body-1">{func.name}</Text>
                                    </Box>
                                ))}
                            </Box>
                        )}
                        {showSuggestions && suggestions.length === 0 && functionName.trim() && !loadingFunctions && (
                            <Box
                                style={{
                                    position: 'absolute',
                                    top: '100%',
                                    left: 0,
                                    right: 0,
                                    background: 'var(--g-color-base-float)',
                                    border: '1px solid var(--g-color-line-generic)',
                                    borderRadius: '4px',
                                    marginTop: '4px',
                                    padding: '8px 12px',
                                    zIndex: 1000,
                                }}
                            >
                                <Text variant="body-2" color="secondary">
                                    No matching functions. You can still use this name.
                                </Text>
                            </Box>
                        )}
                    </div>
                </FormField>
            </Box>
        </FormDialog>
    );
};

