import React, { useEffect, useState } from 'react';
import type { Rule } from '../../../api/acl';
import { toaster } from '../../../utils';
import { rulesToDiffYaml, rulesToYamlObjects } from './SaveDiffModal';
import YamlIOModal from '../../../components/YamlIOModal';
import type { ParseProgress } from '../../../components/YamlIOModal';
import YamlImportWorker from './yamlImport.worker.ts?worker';

/** Worker message types sent from worker to main thread. */
type WorkerMessage =
    | { type: 'progress'; stage: 'yaml' | 'rules'; done: number; total: number }
    | { type: 'done'; rules: Rule[] }
    | { type: 'error'; message: string };

/**
 * Parse YAML or JSON text in a dedicated Web Worker, calling onProgress for each
 * progress event. Resolves with the parsed Rule array, rejects on error.
 * The worker is terminated after the promise settles.
 */
const parseYamlToRulesAsync = (
    text: string,
    onProgress: (p: ParseProgress) => void,
    format: 'yaml' | 'json',
): Promise<Rule[]> => {
    return new Promise((resolve, reject) => {
        const worker = new YamlImportWorker();

        worker.onmessage = (e: MessageEvent<WorkerMessage>): void => {
            const msg = e.data;
            if (msg.type === 'progress') {
                onProgress({ stage: msg.stage, done: msg.done, total: msg.total });
            } else if (msg.type === 'done') {
                worker.terminate();
                resolve(msg.rules);
            } else if (msg.type === 'error') {
                worker.terminate();
                reject(new Error(msg.message));
            }
        };

        worker.onerror = (err: ErrorEvent): void => {
            worker.terminate();
            reject(new Error(err.message));
        };

        worker.postMessage({ type: 'parse', text, format });
    });
};

/** Import mode: replace all existing rules or append to them. */
export type ImportMode = 'replace' | 'append';

interface YamlIOProps {
    configName: string;
    rules: Rule[];
    onImport: (configName: string, rules: Rule[], mode: ImportMode) => void;
}

const rulesToInAclJson = (configName: string, rules: Rule[]): string =>
    JSON.stringify({ name: configName, rules: rulesToYamlObjects(rules) }, null, 2) + '\n';

/** YAML/JSON import/export controls for the ACL NG page header. */
const YamlIO: React.FC<YamlIOProps> = ({ configName, rules, onImport }) => {
    const [mode, setMode] = useState<ImportMode>('replace');

    useEffect(() => {
        setMode('replace');
    }, [configName]);

    const handleImportAsync = async (
        text: string,
        onProgress: (p: ParseProgress) => void,
        format: 'yaml' | 'json',
    ): Promise<void> => {
        const parsed = await parseYamlToRulesAsync(text, onProgress, format);
        onImport(configName, parsed, mode);
        const modeLabel = mode === 'replace' ? 'replace' : 'append';
        toaster.success('acl-yaml-import', `Imported ${parsed.length} rules (${modeLabel}).`);
    };

    const importExtraControls = (
        <div style={{ display: 'flex', gap: 4 }}>
            <button
                type="button"
                className={mode === 'replace' ? 'fw-btn fw-btn--sm' : 'fw-btn fw-btn--ghost fw-btn--sm'}
                onClick={() => setMode('replace')}
            >
                Replace all
            </button>
            <button
                type="button"
                className={mode === 'append' ? 'fw-btn fw-btn--sm' : 'fw-btn fw-btn--ghost fw-btn--sm'}
                onClick={() => setMode('append')}
            >
                Append
            </button>
        </div>
    );

    return (
        <YamlIOModal
            configName={configName}
            itemCount={rules.length}
            itemLabel="rules"
            exportYaml={() => rulesToDiffYaml(rules)}
            exportJson={() => rulesToInAclJson(configName, rules)}
            onImportAsync={handleImportAsync}
            toastPrefix="acl-yaml"
            importPlaceholder={
                'rules:\n' +
                '  - srcs:\n' +
                '      - 192.0.2.0/24\n' +
                '    dsts:\n' +
                '      - 192.0.3.0/24\n' +
                '    dst_port_ranges:\n' +
                '      - "80-80"\n' +
                '    proto_ranges:\n' +
                '      - "1536-1791"\n' +
                '    actions:\n' +
                '      - kind: ACTION_KIND_PASS'
            }
            exportFooterHint="Exports current draft rules (unsaved changes included)."
            importFooterHint={`Loads into "${configName}" as draft — review before save.`}
            importButtonLabel="Load as draft"
            importExtraControls={importExtraControls}
            supportJson
        />
    );
};

export default YamlIO;
