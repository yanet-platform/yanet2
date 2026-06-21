import React from 'react';
import type { PrefixRowItem } from './types';
import { rowsToYaml, yamlToRows } from './yaml';
import DraftYamlIO from '@yanet/core/components/DraftYamlIO';

interface PrefixYamlIOProps {
    configName: string;
    rows: PrefixRowItem[];
    onImport: (rows: PrefixRowItem[], mode: 'replace' | 'append') => void;
    disabled?: boolean;
}

/** YAML import/export controls for the decap page header. */
const PrefixYamlIO: React.FC<PrefixYamlIOProps> = ({ configName, rows, onImport, disabled }) => (
    <DraftYamlIO<PrefixRowItem>
        configName={configName}
        rows={rows}
        onImport={onImport}
        itemLabel="prefixes"
        downloadPrefix={`decap-${configName}`}
        toastPrefix="prefix-yaml"
        importPlaceholder={`config: ${configName}\nprefixes:\n  - 10.0.0.0/8\n  - 2a02:6b8::/32`}
        exportYaml={() => rowsToYaml(configName, rows)}
        parseImport={yamlToRows}
        disabled={disabled}
    />
);

export default PrefixYamlIO;
