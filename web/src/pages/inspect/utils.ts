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

