import React from 'react';
import type { FIBRowItem } from './types';
import { rowsToYaml, yamlToRows } from './yaml';
import DraftYamlIO from '@yanet/core/components/DraftYamlIO';

interface FIBYamlIOProps {
    configName: string;
    rows: FIBRowItem[];
    onImport: (rows: FIBRowItem[], mode: 'replace' | 'append') => void;
    disabled?: boolean;
}

/** YAML import/export controls for the FIB page header. */
const FIBYamlIO: React.FC<FIBYamlIOProps> = ({ configName, rows, onImport, disabled }) => (
    <DraftYamlIO<FIBRowItem>
        configName={configName}
        rows={rows}
        onImport={onImport}
        itemLabel="routes"
        downloadPrefix={`fib-${configName}`}
        toastPrefix="fib-yaml"
        importPlaceholder={`config: ${configName}\nroutes:\n  - from: 10.0.0.0\n    to: 10.0.0.255\n    dst_mac: 52:54:00:00:1c:57\n    src_mac: 52:54:00:12:34:56\n    device: eth0\n    counter: ""`}
        exportYaml={() => rowsToYaml(configName, rows)}
        parseImport={yamlToRows}
        disabled={disabled}
    />
);

export default FIBYamlIO;
