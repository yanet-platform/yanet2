import React, { useState, useCallback, useEffect } from 'react';
import { Dialog, TextInput, Box, Text } from '@gravity-ui/uikit';
import type { FunctionEdge } from '../types';

export interface WeightEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (weights: Map<string, string>) => void;
    edges: FunctionEdge[];
}

export const WeightEditorDialog: React.FC<WeightEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    edges,
}) => {
    const [weights, setWeights] = useState<Map<string, string>>(new Map());
    
    useEffect(() => {
        if (open) {
            const initialWeights = new Map<string, string>();
            for (const edge of edges) {
                const weight = edge.data?.weight;
                initialWeights.set(edge.id, String(weight ?? '1'));
            }
            setWeights(initialWeights);
        }
    }, [open, edges]);
    
    const handleWeightChange = useCallback((edgeId: string, value: string) => {
        setWeights(prev => {
            const newWeights = new Map(prev);
            newWeights.set(edgeId, value);
            return newWeights;
        });
    }, []);
    
    const handleConfirm = useCallback(() => {
        onConfirm(weights);
    }, [weights, onConfirm]);
    
    useEffect(() => {
        if (!open) return;
        
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                handleConfirm();
            }
        };
        
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, handleConfirm]);
    
    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Edit Chain Weights" />
            <Dialog.Body>
                <Box style={{ 
                    display: 'flex', 
                    flexDirection: 'column', 
                    gap: '12px', 
                    width: '400px', 
                    maxWidth: '90vw',
                    maxHeight: '400px',
                    overflowY: 'auto',
                }}>
                    {edges.length === 0 ? (
                        <Text color="secondary">No chains connected to input</Text>
                    ) : (
                        edges.map((edge, index) => (
                            <Box 
                                key={edge.id} 
                                style={{ 
                                    display: 'flex', 
                                    alignItems: 'center', 
                                    gap: '8px',
                                }}
                            >
                                <Text 
                                    variant="body-1" 
                                    style={{ 
                                        flexShrink: 0,
                                        overflow: 'hidden',
                                        textOverflow: 'ellipsis',
                                        whiteSpace: 'nowrap',
                                        maxWidth: '200px',
                                    }}
                                >
                                    {edge.data?.chainName || `Chain ${index + 1}`}
                                </Text>
                                <Box 
                                    style={{ 
                                        flex: 1, 
                                        borderBottom: '1px dotted var(--g-color-line-generic)',
                                        minWidth: '20px',
                                        height: '1px',
                                        alignSelf: 'center',
                                    }} 
                                />
                                <TextInput
                                    value={weights.get(edge.id) || '1'}
                                    onUpdate={(value) => handleWeightChange(edge.id, value)}
                                    placeholder="Weight"
                                    type="number"
                                    min={0}
                                    style={{ width: '80px', flexShrink: 0 }}
                                />
                            </Box>
                        ))
                    )}
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                textButtonApply="Save"
                propsButtonApply={{ view: 'action' as const }}
            />
        </Dialog>
    );
};
