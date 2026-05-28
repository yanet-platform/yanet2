import React, { useState } from 'react';
import type { PrefixRowItem } from './types';
import { rowsToYaml, yamlToRows } from './yaml';
import { toaster } from '../../../utils';
import YamlIOModal from '../../../components/YamlIOModal';

interface PrefixYamlIOProps {
    configName: string;
    rows: PrefixRowItem[];
    onImport: (rows: PrefixRowItem[], mode: 'replace' | 'append') => void;
}

/** YAML import/export controls for the decap page header. */
const PrefixYamlIO: React.FC<PrefixYamlIOProps> = ({ configName, rows, onImport }) => {
    const [importMode, setImportMode] = useState<'replace' | 'append'>('replace');

    const handleImport = (text: string): void => {
        const imported = yamlToRows(text);
        onImport(imported, importMode);
        toaster.success('prefix-yaml-import', `Imported ${imported.length} prefixes (${importMode}).`);
    };

    const importExtraControls = (
        <>
            <div style={{ flex: 1 }} />
            <span style={{ fontSize: 12, color: 'var(--fw-text-3)' }}>Mode:</span>
            <button
                type="button"
                className={`fw-btn fw-btn--sm${importMode === 'replace' ? '' : ' fw-btn--ghost'}`}
                onClick={() => setImportMode('replace')}
            >
                Replace all
            </button>
            <button
                type="button"
                className={`fw-btn fw-btn--sm${importMode === 'append' ? '' : ' fw-btn--ghost'}`}
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
            itemLabel="prefixes"
            downloadPrefix={`decap-${configName}`}
            exportYaml={() => rowsToYaml(configName, rows)}
            onImport={handleImport}
            toastPrefix="prefix-yaml"
            importPlaceholder={`config: ${configName}\nprefixes:\n  - 10.0.0.0/8\n  - 2a02:6b8::/32`}
            exportFooterHint="Exports current draft (uncommitted changes included)."
            importFooterHint="Loads into current config as draft. Use Commit to push to server."
            importButtonLabel="Load as draft"
            importExtraControls={importExtraControls}
        />
    );
};

export default PrefixYamlIO;
