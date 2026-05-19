import React, { useEffect, useRef, useState } from 'react';
import yaml from 'js-yaml';
import { Button, Icon } from '@gravity-ui/uikit';
import { ArrowDownToLine, ArrowUpFromLine } from '@gravity-ui/icons';
import type { Rule } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';
import { toaster } from '../../../utils';
import { parseCidrsToIPNets } from './hooks';
import { rulesToDiffYaml } from './SaveDiffModal';

/** Raw shape of a rule entry in the new YAML schema. */
interface YamlVlanRange {
    from: number;
    to: number;
}

interface YamlRule {
    target: string;
    counter?: string;
    vlan_ranges?: YamlVlanRange[];
    srcs?: string[] | null;
    dsts?: string[] | null;
    devices?: string[] | null;
    mode?: string;
}

/** Parse a YAML string into rules using the canonical schema.
 *
 * Top-level key is `rules`. Config name comes from outside the YAML (the import UI).
 * Returns the parsed rules array on success, throws with a descriptive message on failure.
 */
export const parseYamlToRules = (text: string): Rule[] => {
    let parsed: unknown;
    try {
        parsed = yaml.load(text);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (!parsed || typeof parsed !== 'object') {
        throw new Error('Expected a YAML object with a "rules" list.');
    }

    const doc = parsed as Record<string, unknown>;

    if (!Array.isArray(doc['rules'])) {
        throw new Error('Expected a top-level "rules" list.');
    }

    const modeMap: Record<string, ForwardMode> = {
        IN: ForwardMode.IN,
        OUT: ForwardMode.OUT,
        NONE: ForwardMode.NONE,
        // Capitalized variants from the canonical schema.
        In: ForwardMode.IN,
        Out: ForwardMode.OUT,
        None: ForwardMode.NONE,
    };

    const rules: Rule[] = (doc['rules'] as unknown[]).map((r: unknown): Rule => {
        if (!r || typeof r !== 'object') {
            return { action: { target: '', mode: ForwardMode.NONE } };
        }
        const row = r as YamlRule;

        const target = typeof row.target === 'string' ? row.target : '';
        const counter = typeof row.counter === 'string' ? row.counter : undefined;
        const modeRaw = typeof row.mode === 'string' ? row.mode : 'None';
        const mode = modeMap[modeRaw] ?? ForwardMode.NONE;

        const devicesRaw = Array.isArray(row.devices) ? row.devices : [];
        const devices = (devicesRaw as unknown[])
            .filter((d): d is string => typeof d === 'string')
            .map(name => ({ name }));

        const vlanRangesRaw = Array.isArray(row.vlan_ranges) ? row.vlan_ranges : [];
        const vlan_ranges = (vlanRangesRaw as unknown[]).map((vr: unknown) => {
            if (!vr || typeof vr !== 'object') return { from: 0, to: 0 };
            const v = vr as Record<string, unknown>;
            return {
                from: typeof v['from'] === 'number' ? v['from'] : 0,
                to: typeof v['to'] === 'number' ? v['to'] : 0,
            };
        });

        const srcsRaw = Array.isArray(row.srcs) ? row.srcs : [];
        const srcs = parseCidrsToIPNets(
            (srcsRaw as unknown[]).filter((s): s is string => typeof s === 'string'),
        );

        const dstsRaw = Array.isArray(row.dsts) ? row.dsts : [];
        const dsts = parseCidrsToIPNets(
            (dstsRaw as unknown[]).filter((s): s is string => typeof s === 'string'),
        );

        return { action: { target, mode, counter }, devices, vlan_ranges, srcs, dsts };
    });

    return rules;
};

interface YamlIOProps {
    configName: string;
    /** Draft rules for the current config (used for export). */
    rules: Rule[];
    /** Called when user imports rules into a config. Receives the target config name and parsed rules. */
    onImport: (configName: string, rules: Rule[]) => void;
}

/** YAML import/export controls rendered inline in the page header. */
const YamlIO: React.FC<YamlIOProps> = ({ configName, rules, onImport }) => {
    const [showModal, setShowModal] = useState<'import' | 'export' | null>(null);
    const [text, setText] = useState('');
    const [importConfigName, setImportConfigName] = useState('');
    const [parseError, setParseError] = useState<string | null>(null);
    const textareaRef = useRef<HTMLTextAreaElement>(null);

    useEffect(() => {
        if (showModal === 'export') {
            setText(rulesToDiffYaml(rules));
        } else if (showModal === 'import') {
            setText('');
            setImportConfigName(configName);
            setParseError(null);
        }
    // Intentionally re-run only when modal opens, not on every rules update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [showModal]);

    useEffect(() => {
        if (showModal === null) return;
        const onKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') closeModal();
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [showModal]);

    const handleDownload = (): void => {
        const blob = new Blob([text], { type: 'text/yaml' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `${configName}.yaml`;
        a.click();
        URL.revokeObjectURL(url);
        toaster.success('fwng-yaml-download', 'Download started.');
    };

    const handleCopy = async (): Promise<void> => {
        try {
            await navigator.clipboard.writeText(text);
            toaster.success('fwng-yaml-copy', 'Copied to clipboard.');
        } catch {
            toaster.error('fwng-yaml-copy-fail', 'Copy failed.');
        }
    };

    const handleImport = (): void => {
        setParseError(null);
        try {
            const parsed = parseYamlToRules(text);
            const targetConfig = importConfigName.trim() || configName;
            onImport(targetConfig, parsed);
            setShowModal(null);
            toaster.success('fwng-yaml-import', `Imported ${parsed.length} rules into "${targetConfig}".`);
        } catch (e) {
            setParseError((e as Error).message);
        }
    };

    const handleFileLoad = (file: File): void => {
        const reader = new FileReader();
        reader.onload = (e) => setText(e.target?.result as string ?? '');
        reader.readAsText(file);
    };

    const closeModal = (): void => {
        setShowModal(null);
        setParseError(null);
    };

    return (
        <>
            <Button view="outlined" onClick={() => setShowModal('import')}>
                <Icon data={ArrowDownToLine} size={14} />
                Import YAML
            </Button>
            <Button view="outlined" onClick={() => setShowModal('export')}>
                <Icon data={ArrowUpFromLine} size={14} />
                Export YAML
            </Button>

            {showModal && (
                <div className="fwng-modal-backdrop" onClick={closeModal}>
                    <div className="fwng-modal" onClick={(e) => e.stopPropagation()}>
                        <header className="fwng-modal__head">
                            <div className="fwng-modal__title-row">
                                <span className="fwng-modal__title">
                                    {showModal === 'import' ? 'Import YAML' : 'Export YAML'}
                                </span>
                                {showModal === 'export' && (
                                    <span className="fwng-modal__meta">
                                        {configName} · {rules.length} rules
                                    </span>
                                )}
                            </div>
                            <button
                                type="button"
                                className="fwng-icon-btn"
                                onClick={closeModal}
                                aria-label="Close"
                            >
                                ✕
                            </button>
                        </header>

                        <div className="fwng-modal__body">
                            {showModal === 'import' && (
                                <>
                                    <div className="fwng-modal__import-header">
                                        <label
                                            className="fwng-btn fwng-btn--ghost fwng-btn--sm"
                                            style={{ cursor: 'pointer' }}
                                        >
                                            Choose file
                                            <input
                                                type="file"
                                                accept=".yaml,.yml,text/*"
                                                style={{ display: 'none' }}
                                                onChange={(e) => {
                                                    const f = e.target.files?.[0];
                                                    if (f) handleFileLoad(f);
                                                }}
                                            />
                                        </label>
                                        <span className="fwng-modal__or">or paste below</span>
                                    </div>
                                    <div className="fwng-field" style={{ marginBottom: 8 }}>
                                        <label className="fwng-field__label" htmlFor="fwng-import-config-name">
                                            Config name
                                        </label>
                                        <input
                                            id="fwng-import-config-name"
                                            className="fwng-input"
                                            type="text"
                                            value={importConfigName}
                                            onChange={(e) => setImportConfigName(e.target.value)}
                                            placeholder={configName}
                                        />
                                        <span className="fwng-field__hint">
                                            Rules will be imported into this config (creates it locally if new).
                                        </span>
                                    </div>
                                </>
                            )}
                            <textarea
                                ref={textareaRef}
                                className="fwng-code-area"
                                value={text}
                                onChange={(e) => {
                                    setText(e.target.value);
                                    setParseError(null);
                                }}
                                placeholder={showModal === 'import'
                                    ? 'rules:\n  - target: eth0\n    mode: Out\n    srcs:\n      - 10.0.0.0/8'
                                    : ''}
                                spellCheck={false}
                            />
                            {parseError && (
                                <div className="fwng-field__error" style={{ marginTop: 6, color: 'var(--g-color-text-danger)', fontSize: 12 }}>
                                    {parseError}
                                </div>
                            )}
                        </div>

                        <footer className="fwng-modal__foot">
                            <span className="fwng-modal__foot-hint">
                                {showModal === 'export'
                                    ? 'Exports current draft rules (unsaved changes included).'
                                    : 'Importing replaces all rules in the target config locally. Use Save to push to the server.'}
                            </span>
                            <div className="fwng-modal__foot-actions">
                                <button
                                    type="button"
                                    className="fwng-btn fwng-btn--ghost"
                                    onClick={closeModal}
                                >
                                    Close
                                </button>
                                {showModal === 'export' ? (
                                    <>
                                        <button
                                            type="button"
                                            className="fwng-btn fwng-btn--ghost"
                                            onClick={handleCopy}
                                        >
                                            Copy
                                        </button>
                                        <button
                                            type="button"
                                            className="fwng-btn fwng-btn--primary"
                                            onClick={handleDownload}
                                        >
                                            Download .yaml
                                        </button>
                                    </>
                                ) : (
                                    <button
                                        type="button"
                                        className="fwng-btn fwng-btn--primary"
                                        onClick={handleImport}
                                        disabled={!text.trim()}
                                    >
                                        Import
                                    </button>
                                )}
                            </div>
                        </footer>
                    </div>
                </div>
            )}
        </>
    );
};

export default YamlIO;
