import React, { useState, useEffect } from 'react';
import { pdumpApi, parseModeFlags, modeFlagsToNumber, type PdumpConfig } from '../../../api/pdump';
import { toaster } from '../../../utils';
import BpfTokens from './BpfTokens';
import PdumpModal from './PdumpModal';
import './pdump.scss';

const BPF_SUGGESTIONS = [
    'tcp port 80',
    'tcp port 443',
    'udp port 53',
    'icmp',
    'icmp6',
    'host 2a02:6b8::2:242',
    'src host 10.0.0.1',
    'tcp and dst port 22',
    'ip6 and not icmp6',
    'ether proto 0x86dd',
];

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
        setModes(prev =>
            prev.includes(mode) ? prev.filter(m => m !== mode) : [...prev, mode]
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

    const valid = (isCreate ? configName.trim().length > 0 : true) && filter.trim().length > 0 && modes.length > 0;
    const title = isCreate ? 'Create Pdump Configuration' : `Edit ${initialConfigName}`;

    if (!open) return null;

    const footer = (
        <>
            <button type="button" className="fw-btn fw-btn--ghost" onClick={onClose} disabled={loading}>
                Cancel
            </button>
            <button
                type="button"
                className="fw-btn fw-btn--primary"
                onClick={handleConfirm}
                disabled={!valid || loading}
                style={{ opacity: valid && !loading ? 1 : 0.5 }}
            >
                {isCreate ? 'Create' : 'Save'}
            </button>
        </>
    );

    return (
        <PdumpModal title={title} width="620px" onClose={onClose} footer={footer}>
            {isCreate && (
                <div className="pdump-field">
                    <label className="pdump-field__label">
                        Configuration Name<span className="pdump-field__req">*</span>
                    </label>
                    <input
                        className="pdump-input pdump-input--mono"
                        placeholder="pdump:main"
                        value={configName}
                        onChange={e => setConfigName(e.target.value)}
                        autoFocus
                    />
                    <span className="pdump-field__hint">
                        Unique identifier (e.g., 'pdump:main', 'pdump:dns-debug')
                    </span>
                </div>
            )}

            <div className="pdump-field">
                <label className="pdump-field__label">BPF Filter</label>
                <input
                    className="pdump-input pdump-bpf-input"
                    placeholder="tcp port 80"
                    value={filter}
                    onChange={e => setFilter(e.target.value)}
                />
                <span className="pdump-field__hint">
                    tcpdump-style expression. Live preview:{filter && (
                        <span style={{ marginLeft: 8 }}><BpfTokens expr={filter} /></span>
                    )}
                </span>
                <div className="pdump-bpf-suggestions">
                    {BPF_SUGGESTIONS.map(ex => (
                        <span
                            key={ex}
                            className="pdump-bpf-suggestion"
                            onClick={() => setFilter(ex)}
                        >
                            {ex}
                        </span>
                    ))}
                </div>
            </div>

            <div className="pdump-field">
                <label className="pdump-field__label">Capture Mode</label>
                <div className="pdump-mode-checks">
                    {(['INPUT', 'DROP'] as const).map(m => (
                        <div
                            key={m}
                            className={`pdump-mode-check${modes.includes(m) ? ' pdump-mode-check--checked' : ''}`}
                            onClick={() => handleModeToggle(m)}
                        >
                            <div className="pdump-mode-check__box">
                                {modes.includes(m) && <span className="pdump-mode-check__tick">✓</span>}
                            </div>
                            <div className="pdump-mode-check__text">
                                <div className="pdump-mode-check__label">{m}</div>
                                <div className="pdump-mode-check__hint">
                                    {m === 'INPUT' ? 'All packets entering the module' : 'Only packets the pipeline drops'}
                                </div>
                            </div>
                        </div>
                    ))}
                </div>
                <span className="pdump-field__hint">Bitfield — both can be enabled simultaneously</span>
            </div>

            <div className="pdump-field-row">
                <div className="pdump-field">
                    <label className="pdump-field__label">Snaplen</label>
                    <input
                        className="pdump-input pdump-input--mono"
                        type="number"
                        value={snaplen}
                        placeholder="65535"
                        onChange={e => setSnaplen(e.target.value)}
                    />
                    <span className="pdump-field__hint">Max bytes captured per packet</span>
                </div>
                <div className="pdump-field">
                    <label className="pdump-field__label">Ring Size</label>
                    <input
                        className="pdump-input pdump-input--mono"
                        type="number"
                        value={ringSize}
                        placeholder="1048576"
                        onChange={e => setRingSize(e.target.value)}
                    />
                    <span className="pdump-field__hint">Per-worker ring buffer (bytes)</span>
                </div>
            </div>
        </PdumpModal>
    );
};
