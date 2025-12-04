type NullableString = string | null | undefined;
type NullableNumber = number | null | undefined;
type NullableBoolean = boolean | null | undefined;

export const compareNullableStrings = (a: NullableString, b: NullableString): number => {
    return (a ?? '').localeCompare(b ?? '');
};

export const compareNullableNumbers = (a: NullableNumber, b: NullableNumber): number => {
    const safeA = a ?? Number.NEGATIVE_INFINITY;
    const safeB = b ?? Number.NEGATIVE_INFINITY;
    if (safeA === safeB) {
        return 0;
    }
    return safeA < safeB ? -1 : 1;
};

export const compareBooleans = (a: NullableBoolean, b: NullableBoolean): number => {
    const numericA = a ? 1 : 0;
    const numericB = b ? 1 : 0;
    return numericA - numericB;
};

export const getUnixSecondsValue = (value?: string | number): number | null => {
    if (value === undefined || value === null) {
        return null;
    }

    const parsedValue = typeof value === 'string' ? Number(value) : value;
    return Number.isFinite(parsedValue) ? parsedValue : null;
};

export const formatUnixSeconds = (value?: string | number): string => {
    const timestamp = getUnixSecondsValue(value);
    if (timestamp === null) {
        return '-';
    }

    const date = new Date(timestamp * 1000);
    return date.toLocaleString();
};

type BigIntLike = string | number | bigint | null | undefined;

export const getBigIntValue = (value: BigIntLike): bigint | null => {
    if (value === undefined || value === null) {
        return null;
    }

    try {
        if (typeof value === 'bigint') {
            return value;
        }
        return BigInt(value);
    } catch {
        return null;
    }
};

export const compareBigIntValues = (a: BigIntLike, b: BigIntLike): number => {
    const valA = getBigIntValue(a);
    const valB = getBigIntValue(b);

    if (valA === null && valB === null) {
        return 0;
    }
    if (valA === null) {
        return -1;
    }
    if (valB === null) {
        return 1;
    }
    if (valA === valB) {
        return 0;
    }
    return valA < valB ? -1 : 1;
};
