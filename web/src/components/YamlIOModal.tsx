import React, { useEffect, useRef, useState } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { ArrowDownToLine, ArrowUpFromLine } from '@gravity-ui/icons';
import { toaster, copyToClipboard } from '../utils';

export type YamlIOMode = 'import' | 'export' | null;

/** Files larger than this threshold suppress the textarea preview. */
const LARGE_FILE_THRESHOLD = 1_000_000;

/** Progress information reported by an async import worker. */
export interface ParseProgress {
    stage: 'yaml' | 'rules' | string;
    done: number;
    total: number;
}

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
    /**
     * Parse the textarea text and apply the import synchronously. Should throw on failure.
     * Either onImport or onImportAsync must be supplied.
     */
    onImport?: (text: string) => void;
    /**
     * Parse the textarea text asynchronously. Receives an onProgress callback that the
     * caller should invoke for each progress event. Should reject with an Error on failure.
     * Either onImport or onImportAsync must be supplied.
     */
    onImportAsync?: (text: string, onProgress: (p: ParseProgress) => void, format: 'yaml' | 'json') => Promise<void>;
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
    /**
     * Opt-in to JSON support. When true: the file picker accepts .json, the export modal
     * shows a JSON/YAML format toggle, import auto-detects format by extension or content
     * sniff, and the detected format is passed to onImportAsync. Default: false.
     */
    supportJson?: boolean;
    /**
     * JSON serialiser for export. Only consulted when supportJson is true.
     * If not provided alongside supportJson, the toggle is hidden and export falls back to YAML.
     */
    exportJson?: () => string;
}

/**
 * Reusable YAML (and optionally JSON) import/export modal chrome used by multi-config draft pages.
 * Renders Import and Export buttons; clicking either opens the modal.
 * Callers supply the serialisation and parsing logic via props.
 * Consumes fw-* CSS classes from draft-page.scss.
 */
const YamlIOModal: React.FC<YamlIOModalProps> = ({
    configName,
    itemCount,
    itemLabel = 'items',
    downloadPrefix,
    exportYaml,
    exportJson,
    onImport,
    onImportAsync,
    toastPrefix,
    importPlaceholder,
    exportFooterHint,
    importFooterHint,
    importButtonLabel = 'Import',
    importExtraControls,
    supportJson = false,
}) => {
    const [showModal, setShowModal] = useState<YamlIOMode>(null);
    // textContent holds the actual text string; textarea only shows it when small.
    const textContent = useRef('');
    // Filename from the last file-picker selection; null when user pastes manually.
    const pickedFilename = useRef<string | null>(null);
    // Preview-safe string bound to the textarea (empty for large files).
    const [previewText, setPreviewText] = useState('');
    const [isLargeFile, setIsLargeFile] = useState(false);
    const [loadProgress, setLoadProgress] = useState<number | null>(null);
    const [isParsing, setIsParsing] = useState(false);
    const [parseProgress, setParseProgress] = useState<ParseProgress | null>(null);
    const [parseError, setParseError] = useState<string | null>(null);
    const textareaRef = useRef<HTMLTextAreaElement>(null);

    // Export format toggle state (only meaningful when supportJson && exportJson).
    const showFormatToggle = supportJson && exportJson !== undefined;
    const [exportFormat, setExportFormat] = useState<'json' | 'yaml'>('json');

    const applyText = (content: string): void => {
        textContent.current = content;
        if (content.length > LARGE_FILE_THRESHOLD) {
            setIsLargeFile(true);
            setPreviewText('');
        } else {
            setIsLargeFile(false);
            setPreviewText(content);
        }
    };

    const computeExportText = (fmt: 'json' | 'yaml'): string => {
        if (fmt === 'json' && exportJson) {
            return exportJson();
        }
        return exportYaml();
    };

    useEffect(() => {
        if (showModal === 'export') {
            applyText(computeExportText(exportFormat));
        } else if (showModal === 'import') {
            textContent.current = '';
            pickedFilename.current = null;
            setPreviewText('');
            setIsLargeFile(false);
            setLoadProgress(null);
            setParseError(null);
            setIsParsing(false);
            setParseProgress(null);
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

    const handleExportFormatChange = (fmt: 'json' | 'yaml'): void => {
        setExportFormat(fmt);
        applyText(computeExportText(fmt));
    };

    const detectImportFormat = (text: string, filename?: string): 'yaml' | 'json' => {
        if (filename && filename.toLowerCase().endsWith('.json')) return 'json';
        const first = text.trimStart()[0];
        if (first === '{' || first === '[') return 'json';
        return 'yaml';
    };

    const handleDownload = (): void => {
        const filename = downloadPrefix ?? configName;
        const isJson = supportJson && exportFormat === 'json';
        const ext = isJson ? 'json' : 'yaml';
        const mimeType = isJson ? 'application/json' : 'text/yaml';
        const blob = new Blob([textContent.current], { type: mimeType });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `${filename}.${ext}`;
        a.click();
        URL.revokeObjectURL(url);
        toaster.success(`${toastPrefix}-download`, 'Download started.');
    };

    const handleCopy = async (): Promise<void> => {
        try {
            await copyToClipboard(textContent.current);
            toaster.success(`${toastPrefix}-copy`, 'Copied to clipboard.');
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            toaster.error(`${toastPrefix}-copy-fail`, `Copy failed: ${msg}`);
        }
    };

    const handleImport = (): void => {
        setParseError(null);
        setParseProgress(null);
        setIsParsing(true);

        if (onImportAsync) {
            const format = supportJson
                ? detectImportFormat(textContent.current, pickedFilename.current ?? undefined)
                : 'yaml';
            onImportAsync(textContent.current, (p) => setParseProgress(p), format)
                .then(() => {
                    setIsParsing(false);
                    setParseProgress(null);
                    setShowModal(null);
                })
                .catch((e: unknown) => {
                    setIsParsing(false);
                    setParseProgress(null);
                    setParseError((e as Error).message);
                });
            return;
        }

        // Defer the parse via setTimeout so the browser can paint the spinner
        // before the synchronous YAML parse blocks the main thread.
        setTimeout(() => {
            try {
                onImport?.(textContent.current);
                setIsParsing(false);
                setShowModal(null);
            } catch (e) {
                setIsParsing(false);
                setParseError((e as Error).message);
            }
        }, 0);
    };

    const handleFileLoad = (file: File): void => {
        pickedFilename.current = file.name;
        setLoadProgress(0);
        setParseError(null);
        const reader = new FileReader();

        reader.onloadstart = (): void => {
            setLoadProgress(0);
        };

        reader.onprogress = (e: ProgressEvent): void => {
            if (e.lengthComputable && e.total > 0) {
                setLoadProgress(Math.round((e.loaded / e.total) * 100));
            }
        };

        reader.onload = (e): void => {
            const content = e.target?.result as string ?? '';
            setLoadProgress(null);
            applyText(content);
        };

        reader.onerror = (): void => {
            setLoadProgress(null);
            setParseError('Failed to read file.');
        };

        reader.readAsText(file);
    };

    const closeModal = (): void => {
        setShowModal(null);
        setParseError(null);
        setIsParsing(false);
        setLoadProgress(null);
        setParseProgress(null);
    };

    const hasContent = textContent.current.trim().length > 0;

    const importLabel = supportJson ? 'Import' : 'Import YAML';
    const exportLabel = supportJson ? 'Export' : 'Export YAML';
    const fileAccept = supportJson
        ? '.yaml,.yml,.json,application/json,text/*'
        : '.yaml,.yml,text/*';

    const renderParseStatus = (): React.ReactNode => {
        if (!isParsing) return null;

        if (parseProgress) {
            const { stage, done, total } = parseProgress;
            if (stage === 'yaml') {
                const pct = total > 0 ? Math.round((done / total) * 100) : 0;
                return (
                    <div style={{ marginTop: 6 }}>
                        <div style={{ color: 'var(--fw-text-3)', fontSize: 12, marginBottom: 4 }}>
                            Parsing… {done < total ? '' : '100%'}
                        </div>
                        <div className="fw-parse-progress">
                            <div
                                className="fw-parse-progress__bar"
                                style={{ width: `${pct}%` }}
                            />
                        </div>
                    </div>
                );
            }
            if (stage === 'rules') {
                const pct = total > 0 ? Math.round((done / total) * 100) : 0;
                return (
                    <div style={{ marginTop: 6 }}>
                        <div style={{ color: 'var(--fw-text-3)', fontSize: 12, marginBottom: 4 }}>
                            Converting rules: {done.toLocaleString()} / {total.toLocaleString()} ({pct}%)
                        </div>
                        <div className="fw-parse-progress">
                            <div
                                className="fw-parse-progress__bar"
                                style={{ width: `${pct}%` }}
                            />
                        </div>
                    </div>
                );
            }
        }

        return (
            <div style={{ marginTop: 6, color: 'var(--fw-text-3)', fontSize: 12 }}>
                Parsing…
            </div>
        );
    };

    const renderExportFormatToggle = (): React.ReactNode => {
        if (!showFormatToggle) return null;
        return (
            <div style={{ display: 'flex', gap: 4 }}>
                <button
                    type="button"
                    className={exportFormat === 'json' ? 'fw-btn fw-btn--sm' : 'fw-btn fw-btn--ghost fw-btn--sm'}
                    onClick={() => handleExportFormatChange('json')}
                >
                    JSON
                </button>
                <button
                    type="button"
                    className={exportFormat === 'yaml' ? 'fw-btn fw-btn--sm' : 'fw-btn fw-btn--ghost fw-btn--sm'}
                    onClick={() => handleExportFormatChange('yaml')}
                >
                    YAML
                </button>
            </div>
        );
    };

    return (
        <>
            <Button view="outlined" onClick={() => setShowModal('import')}>
                <Icon data={ArrowDownToLine} size={14} />
                {importLabel}
            </Button>
            <Button view="outlined" onClick={() => setShowModal('export')}>
                <Icon data={ArrowUpFromLine} size={14} />
                {exportLabel}
            </Button>

            {showModal && (
                <div className="fw-modal-backdrop" onClick={closeModal}>
                    <div className="fw-modal" onClick={(e) => e.stopPropagation()}>
                        <header className="fw-modal__head">
                            <div className="fw-modal__title-row">
                                <span className="fw-modal__title">
                                    {showModal === 'import' ? importLabel : exportLabel}
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
                                            accept={fileAccept}
                                            style={{ display: 'none' }}
                                            onChange={(e) => {
                                                const f = e.target.files?.[0];
                                                if (f) handleFileLoad(f);
                                            }}
                                        />
                                    </label>
                                    {loadProgress !== null && (
                                        <span className="fw-modal__load-progress">
                                            Reading… {loadProgress}%
                                        </span>
                                    )}
                                    {loadProgress === null && (
                                        <span className="fw-modal__or">or paste below</span>
                                    )}
                                    {importExtraControls}
                                </div>
                            )}
                            {showModal === 'export' && renderExportFormatToggle()}
                            {isLargeFile ? (
                                <div className="fw-modal__large-file-notice">
                                    Large file loaded ({(textContent.current.length / 1_048_576).toFixed(1)} MB) — preview suppressed to avoid browser slowdown.
                                </div>
                            ) : (
                                <textarea
                                    ref={textareaRef}
                                    className="fw-code-area"
                                    value={previewText}
                                    onChange={(e) => {
                                        const v = e.target.value;
                                        textContent.current = v;
                                        pickedFilename.current = null;
                                        setPreviewText(v);
                                        setParseError(null);
                                    }}
                                    placeholder={showModal === 'import' ? importPlaceholder : ''}
                                    spellCheck={false}
                                />
                            )}
                            {renderParseStatus()}
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
                                            {showFormatToggle
                                                ? `Download .${exportFormat}`
                                                : 'Download .yaml'}
                                        </button>
                                    </>
                                ) : (
                                    <button
                                        type="button"
                                        className="fw-btn fw-btn--primary"
                                        onClick={handleImport}
                                        disabled={!hasContent || isParsing || loadProgress !== null}
                                    >
                                        {isParsing ? 'Parsing…' : importButtonLabel}
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
