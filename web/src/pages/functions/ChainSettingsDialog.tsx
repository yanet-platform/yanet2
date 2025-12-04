import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Dialog, TextInput } from '@gravity-ui/uikit';
import type { ChainPath } from './Graph';

export interface ChainSettingsDialogProps {
    open: boolean;
    onClose: () => void;
    chains: ChainPath[];
    onSave: (chains: ChainPath[]) => void;
}

interface ChainWeightEntry {
    index: number;
    weight: string;
    modules: string;
}

export const ChainSettingsDialog: React.FC<ChainSettingsDialogProps> = ({
    open,
    onClose,
    chains,
    onSave,
}) => {
    const [entries, setEntries] = useState<ChainWeightEntry[]>([]);

    useEffect(() => {
        if (open) {
            setEntries(
                chains.map((chain, index) => ({
                    index,
                    weight: String(chain.weight),
                    modules: chain.modules.map(m => m.name || m.type || 'unknown').join(' → '),
                }))
            );
        }
    }, [open, chains]);

    const handleWeightChange = useCallback((index: number, value: string) => {
        setEntries(prev =>
            prev.map(entry =>
                entry.index === index ? { ...entry, weight: value } : entry
            )
        );
    }, []);

    const handleSave = useCallback(() => {
        const updatedChains = chains.map((chain, index) => {
            const entry = entries.find(e => e.index === index);
            const weight = entry ? parseInt(entry.weight, 10) : chain.weight;
            return {
                ...chain,
                weight: isNaN(weight) || weight < 1 ? 1 : weight,
            };
        });
        onSave(updatedChains);
        onClose();
    }, [chains, entries, onSave, onClose]);

    const handleKeyDown = useCallback(
        (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault();
                handleSave();
            }
        },
        [handleSave]
    );

    useEffect(() => {
        if (!open) return;
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [handleKeyDown, open]);

    return (
        <Dialog open={open} onClose={onClose} size="m">
            <Dialog.Header caption="Chain Settings" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', minWidth: '400px' }}>
                    <Text variant="body-1" color="secondary">
                        Configure weights for each chain. Higher weight means more traffic will be routed through that chain.
                    </Text>

                    {entries.length === 0 ? (
                        <Box
                            style={{
                                padding: '24px',
                                textAlign: 'center',
                                border: '1px dashed var(--g-color-line-generic)',
                                borderRadius: '8px',
                            }}
                        >
                            <Text variant="body-1" color="secondary">
                                No chains configured. Create connections from INPUT to OUTPUT.
                            </Text>
                        </Box>
                    ) : (
                        <Box style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                            {entries.map((entry, idx) => (
                                <Box
                                    key={entry.index}
                                    style={{
                                        display: 'flex',
                                        alignItems: 'center',
                                        gap: '12px',
                                        padding: '12px',
                                        backgroundColor: 'var(--g-color-base-simple-hover)',
                                        borderRadius: '8px',
                                    }}
                                >
                                    <Box style={{ flex: 1 }}>
                                        <Text variant="subheader-1" style={{ marginBottom: '4px', display: 'block' }}>
                                            Chain {idx + 1}
                                        </Text>
                                        <Text variant="caption-1" color="secondary" ellipsis>
                                            {entry.modules || '(empty chain)'}
                                        </Text>
                                    </Box>
                                    <Box style={{ width: '100px' }}>
                                        <TextInput
                                            value={entry.weight}
                                            onUpdate={(value) => handleWeightChange(entry.index, value)}
                                            type="number"
                                            placeholder="Weight"
                                            size="m"
                                        />
                                    </Box>
                                </Box>
                            ))}
                        </Box>
                    )}

                    <Text variant="caption-1" color="hint">
                        Tip: Drag from the INPUT block's output anchor to create additional chains.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleSave}
                textButtonApply="Save"
                textButtonCancel="Cancel"
            />
        </Dialog>
    );
};

