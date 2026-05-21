import React, { useEffect, useRef, useState } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { ArrowDownToLine, ArrowUpFromLine } from '@gravity-ui/icons';
import { toaster, copyToClipboard } from '../utils';

export type YamlIOMode = 'import' | 'export' | null;

export interface YamlIOModalProps {
    /** Config name used in export metadata and download filename. */
    configName: string;
    /** Item count shown in export header. */
    itemCount: number;
    /** Label for the item type, e.g. "rules" or "routes". Shown in export meta. */
    itemLabel?: string;
    /** Filename prefix for the downloaded file. Defaults to configName. */
    downloadPrefix?: string;
    /** YAML text for export mode. Computed once when the modal opens. */
    exportYaml: () => string;
    /** Parse the textarea text and apply the import. Should throw on failure. */
    onImport: (text: string) => void;
    /** Toast key prefix, e.g. "fw-yaml" or "fib-yaml". */
    toastPrefix: string;
    /** Placeholder YAML shown in import textarea. */
    importPlaceholder: string;
    /** Hint text shown in the export modal footer. */
    exportFooterHint: string;
    /** Hint text shown in the import modal footer. */
    importFooterHint: string;
    /** Label for the import confirm button. Defaults to "Import". */
    importButtonLabel?: string;
    /** Optional slot rendered inside the import body above the textarea (e.g. mode toggle). */
    importExtraControls?: React.ReactNode;
}

/**
 * Reusable YAML import/export modal chrome used by multi-config draft pages.
 * Renders Import YAML and Export YAML buttons; clicking either opens the modal.
 * Callers supply the serialisation and parsing logic via props.
 * Consumes fw-* CSS classes from forward.scss.
 */
const YamlIOModal: React.FC<YamlIOModalProps> = ({
    configName,
    itemCount,
    itemLabel = 'items',
    downloadPrefix,
    exportYaml,
    onImport,
    toastPrefix,
    importPlaceholder,
    exportFooterHint,
    importFooterHint,
    importButtonLabel = 'Import',
    importExtraControls,
}) => {
    const [showModal, setShowModal] = useState<YamlIOMode>(null);
    const [text, setText] = useState('');
    const [parseError, setParseError] = useState<string | null>(null);
    const textareaRef = useRef<HTMLTextAreaElement>(null);

    useEffect(() => {
        if (showModal === 'export') {
            setText(exportYaml());
        } else if (showModal === 'import') {
            setText('');
            setParseError(null);
        }
    // Intentionally re-run only when modal opens, not on every data update.
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
        const filename = downloadPrefix ?? configName;
        const blob = new Blob([text], { type: 'text/yaml' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `${filename}.yaml`;
        a.click();
        URL.revokeObjectURL(url);
        toaster.success(`${toastPrefix}-download`, 'Download started.');
    };

    const handleCopy = async (): Promise<void> => {
        try {
            await copyToClipboard(text);
            toaster.success(`${toastPrefix}-copy`, 'Copied to clipboard.');
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            toaster.error(`${toastPrefix}-copy-fail`, `Copy failed: ${msg}`);
        }
    };

    const handleImport = (): void => {
        setParseError(null);
        try {
            onImport(text);
            setShowModal(null);
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
                <div className="fw-modal-backdrop" onClick={closeModal}>
                    <div className="fw-modal" onClick={(e) => e.stopPropagation()}>
                        <header className="fw-modal__head">
                            <div className="fw-modal__title-row">
                                <span className="fw-modal__title">
                                    {showModal === 'import' ? 'Import YAML' : 'Export YAML'}
                                </span>
                                {showModal === 'export' && (
                                    <span className="fw-modal__meta">
                                        {configName} · {itemCount} {itemLabel}
                                    </span>
                                )}
                            </div>
                            <button
                                type="button"
                                className="fw-icon-btn"
                                onClick={closeModal}
                                aria-label="Close"
                            >
                                ✕
                            </button>
                        </header>

                        <div className="fw-modal__body">
                            {showModal === 'import' && (
                                <div className="fw-modal__import-header">
                                    <label
                                        className="fw-btn fw-btn--ghost fw-btn--sm"
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
                                    <span className="fw-modal__or">or paste below</span>
                                    {importExtraControls}
                                </div>
                            )}
                            <textarea
                                ref={textareaRef}
                                className="fw-code-area"
                                value={text}
                                onChange={(e) => {
                                    setText(e.target.value);
                                    setParseError(null);
                                }}
                                placeholder={showModal === 'import' ? importPlaceholder : ''}
                                spellCheck={false}
                            />
                            {parseError && (
                                <div style={{ marginTop: 6, color: 'var(--g-color-text-danger)', fontSize: 12 }}>
                                    {parseError}
                                </div>
                            )}
                        </div>

                        <footer className="fw-modal__foot">
                            <span className="fw-modal__foot-hint">
                                {showModal === 'export' ? exportFooterHint : importFooterHint}
                            </span>
                            <div className="fw-modal__foot-actions">
                                <button
                                    type="button"
                                    className="fw-btn fw-btn--ghost"
                                    onClick={closeModal}
                                >
                                    Close
                                </button>
                                {showModal === 'export' ? (
                                    <>
                                        <button
                                            type="button"
                                            className="fw-btn fw-btn--ghost"
                                            onClick={handleCopy}
                                        >
                                            Copy
                                        </button>
                                        <button
                                            type="button"
                                            className="fw-btn fw-btn--primary"
                                            onClick={handleDownload}
                                        >
                                            Download .yaml
                                        </button>
                                    </>
                                ) : (
                                    <button
                                        type="button"
                                        className="fw-btn fw-btn--primary"
                                        onClick={handleImport}
                                        disabled={!text.trim()}
                                    >
                                        {importButtonLabel}
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

export default YamlIOModal;
