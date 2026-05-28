import React, { useState } from 'react';
import type { FIBRowItem } from './types';
import { rowsToYaml, yamlToRows } from './yaml';
import { toaster } from '../../../utils';
import YamlIOModal from '../../../components/YamlIOModal';

interface FIBYamlIOProps {
    configName: string;
    rows: FIBRowItem[];
    onImport: (rows: FIBRowItem[], mode: 'replace' | 'append') => void;
}

/** YAML import/export controls for the FIB page header. */
const FIBYamlIO: React.FC<FIBYamlIOProps> = ({ configName, rows, onImport }) => {
    const [importMode, setImportMode] = useState<'replace' | 'append'>('replace');

    const handleImport = (text: string): void => {
        const imported = yamlToRows(text);
        onImport(imported, importMode);
        toaster.success('fib-yaml-import', `Imported ${imported.length} routes (${importMode}).`);
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
            itemLabel="routes"
            downloadPrefix={`fib-${configName}`}
            exportYaml={() => rowsToYaml(configName, rows)}
            onImport={handleImport}
            toastPrefix="fib-yaml"
            importPlaceholder={`config: ${configName}\nroutes:\n  - prefix: 10.0.0.0/8\n    dst_mac: 52:54:00:00:1c:57\n    src_mac: 52:54:00:12:34:56\n    device: eth0`}
            exportFooterHint="Exports current draft (uncommitted changes included)."
            importFooterHint="Loads into current config as draft. Use Commit to push to server."
            importButtonLabel="Load as draft"
            importExtraControls={importExtraControls}
        />
    );
};

export default FIBYamlIO;
