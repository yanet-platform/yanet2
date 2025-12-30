import { getBigIntValue } from '../../utils/sorting';
import { NAME_MAPPING } from './constants';

const capitalize = (str: string): string => {
    return str.charAt(0).toUpperCase() + str.slice(1);
};

export const formatModuleName = (name: string): string => {
    const lowerName = name.toLowerCase();
    return NAME_MAPPING[lowerName] || capitalize(name);
};

export const formatAgentName = (name: string): string => {
    return formatModuleName(name);
};

export const formatUint64 = (value: string | number | bigint | undefined): string => {
    const parsed = getBigIntValue(value);
    return parsed === null ? '-' : parsed.toString();
};

export const formatBytes = (bytes: bigint): string => {
    if (bytes < BigInt(1024)) {
        return `${bytes} B`;
    }
    if (bytes < BigInt(1024 * 1024)) {
        const kb = Number(bytes) / 1024;
        return `${kb.toFixed(1)} KB`;
    }
    if (bytes < BigInt(1024 * 1024 * 1024)) {
        const mb = Number(bytes) / (1024 * 1024);
        return `${mb.toFixed(1)} MB`;
    }
    if (bytes < BigInt(1024 * 1024 * 1024 * 1024)) {
        const gb = Number(bytes) / (1024 * 1024 * 1024);
        return `${gb.toFixed(2)} GB`;
    }
    const tb = Number(bytes) / (1024 * 1024 * 1024 * 1024);
    return `${tb.toFixed(2)} TB`;
};
