import React, { useState, useEffect } from 'react';
import { TextInput, Checkbox, Flex, Box } from '@gravity-ui/uikit';
import { FormDialog } from '../../components/FormDialog';
import { FormField } from '../../components/FormField';
import { pdumpApi, parseModeFlags, modeFlagsToNumber, type PdumpConfig } from '../../api/pdump';
import { toaster } from '../../utils';
import './pdump.scss';

interface ConfigDialogProps {
    open: boolean;
    onClose: () => void;
    configName?: string;
    initialConfig?: PdumpConfig;
    onSaved: () => void;
    isCreate?: boolean;
}

export const ConfigDialog: React.FC<ConfigDialogProps> = ({
    open,
    onClose,
    configName: initialConfigName,
    initialConfig,
    onSaved,
    isCreate = false,
}) => {
    const [configName, setConfigName] = useState('');
    const [filter, setFilter] = useState('');
    const [modes, setModes] = useState<string[]>([]);
    const [snaplen, setSnaplen] = useState('');
    const [ringSize, setRingSize] = useState('');
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (open) {
            if (isCreate) {
                setConfigName('');
                setFilter('');
                setModes(['INPUT']);
                setSnaplen('');
                setRingSize('');
            } else if (initialConfig) {
                setConfigName(initialConfigName ?? '');
                setFilter(initialConfig.filter ?? '');
                setModes(initialConfig.mode ? parseModeFlags(initialConfig.mode) : []);
                setSnaplen(initialConfig.snaplen?.toString() ?? '');
                setRingSize(initialConfig.ring_size?.toString() ?? '');
            } else {
                setConfigName(initialConfigName ?? '');
                setFilter('');
                setModes(['INPUT']);
                setSnaplen('');
                setRingSize('');
            }
        }
    }, [open, initialConfig, initialConfigName, isCreate]);

    const handleModeToggle = (mode: string) => {
        setModes((prev) =>
            prev.includes(mode)
                ? prev.filter((m) => m !== mode)
                : [...prev, mode]
        );
    };

    const handleConfirm = async () => {
        const targetConfigName = isCreate ? configName : initialConfigName;
        if (!targetConfigName?.trim()) {
            toaster.error('pdump-config-error', 'Configuration name is required', new Error('Name required'));
            return;
        }

        setLoading(true);
        try {
            const config: PdumpConfig = {
                filter: filter || '',
                mode: modeFlagsToNumber(modes),
                snaplen: snaplen ? parseInt(snaplen, 10) : undefined,
                ring_size: ringSize ? parseInt(ringSize, 10) : undefined,
            };

            // Build update mask with all fields that should be updated
            const paths: string[] = ['filter', 'mode'];
            if (config.snaplen !== undefined) {
                paths.push('snaplen');
            }
            if (config.ring_size !== undefined) {
                paths.push('ring_size');
            }

            await pdumpApi.setConfig(targetConfigName, config, { paths });
            toaster.success('pdump-config-saved', isCreate ? 'Configuration created' : 'Configuration saved');
            onSaved();
            onClose();
        } catch (err) {
            toaster.error(
                'pdump-config-error',
                isCreate ? 'Failed to create configuration' : 'Failed to save configuration',
                err instanceof Error ? err : new Error(String(err))
            );
        } finally {
            setLoading(false);
        }
    };

    const title = isCreate ? 'Create Pdump Configuration' : `Edit ${initialConfigName}`;

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title={title}
            confirmText={isCreate ? 'Create' : 'Save'}
            loading={loading}
            width="500px"
        >
            <Flex direction="column" gap={4}>
                {isCreate && (
                    <FormField
                        label="Configuration Name"
                        required
                        hint="Unique name for the pdump configuration (e.g., 'pdump:main')"
                    >
                        <TextInput
                            value={configName}
                            onUpdate={setConfigName}
                            placeholder="pdump:main"
                            size="l"
                            autoFocus
                        />
                    </FormField>
                )}

                <FormField
                    label="Filter"
                    hint="tcpdump-style filter expression (e.g., 'tcp port 80', 'icmp', 'host 192.168.1.1')"
                >
                    <TextInput
                        value={filter}
                        onUpdate={setFilter}
                        placeholder="tcp port 80"
                        size="l"
                    />
                </FormField>

                <FormField label="Capture Mode" hint="INPUT = incoming packets, DROP = dropped packets">
                    <Flex gap={4}>
                        <Checkbox
                            checked={modes.includes('INPUT')}
                            onUpdate={() => handleModeToggle('INPUT')}
                            size="l"
                        >
                            INPUT
                        </Checkbox>
                        <Checkbox
                            checked={modes.includes('DROP')}
                            onUpdate={() => handleModeToggle('DROP')}
                            size="l"
                        >
                            DROP
                        </Checkbox>
                    </Flex>
                </FormField>

                <Box className="config-dialog__row">
                    <Box className="config-dialog__col">
                        <FormField
                            label="Snaplen"
                            hint="Maximum bytes to capture per packet"
                        >
                            <TextInput
                                value={snaplen}
                                onUpdate={setSnaplen}
                                placeholder="65535"
                                type="number"
                                size="l"
                            />
                        </FormField>
                    </Box>
                    <Box className="config-dialog__col">
                        <FormField
                            label="Ring Size"
                            hint="Per-worker ring buffer size"
                        >
                            <TextInput
                                value={ringSize}
                                onUpdate={setRingSize}
                                placeholder="1024"
                                type="number"
                                size="l"
                            />
                        </FormField>
                    </Box>
                </Box>
            </Flex>
        </FormDialog>
    );
};
