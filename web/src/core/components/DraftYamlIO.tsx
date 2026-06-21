import React, { useState } from 'react';
import { toaster } from '../utils';
import YamlIOModal from './YamlIOModal';

export interface DraftYamlIOProps<T> {
    configName: string;
    rows: T[];
    onImport: (rows: T[], mode: 'replace' | 'append') => void;
    itemLabel: string;
    downloadPrefix: string;
    toastPrefix: string;
    importPlaceholder: string;
    exportYaml: () => string;
    parseImport: (text: string) => T[];
    disabled?: boolean;
}

/** Generic YAML import/export controls for draft config pages. */
const DraftYamlIO = <T,>({
    configName,
    rows,
    onImport,
    itemLabel,
    downloadPrefix,
    toastPrefix,
    importPlaceholder,
    exportYaml,
    parseImport,
    disabled,
}: DraftYamlIOProps<T>): React.JSX.Element => {
    const [importMode, setImportMode] = useState<'replace' | 'append'>('replace');

    const handleImport = (text: string): void => {
        const imported = parseImport(text);
        onImport(imported, importMode);
        toaster.success(`${toastPrefix}-import`, `Imported ${imported.length} ${itemLabel} (${importMode}).`);
    };

    const importExtraControls = (
        <>
            <div style={{ flex: 1 }} />
            <span style={{ fontSize: 12, color: 'var(--yn-text-3)' }}>Mode:</span>
            <button
                type="button"
                className={`yn-btn yn-btn--sm${importMode === 'replace' ? '' : ' yn-btn--ghost'}`}
                onClick={() => setImportMode('replace')}
            >
                Replace all
            </button>
            <button
                type="button"
                className={`yn-btn yn-btn--sm${importMode === 'append' ? '' : ' yn-btn--ghost'}`}
                onClick={() => setImportMode('append')}
            >
                Append
            </button>
        </>
    );

    return (
        <YamlIOModal
            configName={configName}
            itemCount={rows.length}
            itemLabel={itemLabel}
            downloadPrefix={downloadPrefix}
            exportYaml={exportYaml}
            onImport={handleImport}
            toastPrefix={toastPrefix}
            importPlaceholder={importPlaceholder}
            exportFooterHint="Exports current draft (uncommitted changes included)."
            importFooterHint="Loads into current config as draft. Use Commit to push to server."
            importButtonLabel="Load as draft"
            importExtraControls={importExtraControls}
            disabled={disabled}
        />
    );
};

export default DraftYamlIO;
