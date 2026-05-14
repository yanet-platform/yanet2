const PKT_TIERS: { value: number; suffix: string; digits: number }[] = [
    { value: 1e18, suffix: 'E', digits: 2 },
    { value: 1e15, suffix: 'P', digits: 2 },
    { value: 1e12, suffix: 'T', digits: 2 },
    { value: 1e9,  suffix: 'G', digits: 2 },
    { value: 1e6,  suffix: 'M', digits: 2 },
    { value: 1e3,  suffix: 'k', digits: 1 },
];

const BYTE_TIERS: { value: number; suffix: string; digits: number }[] = [
    { value: 1024 ** 6, suffix: 'EB', digits: 2 },
    { value: 1024 ** 5, suffix: 'PB', digits: 2 },
    { value: 1024 ** 4, suffix: 'TB', digits: 2 },
    { value: 1024 ** 3, suffix: 'GB', digits: 2 },
    { value: 1024 ** 2, suffix: 'MB', digits: 2 },
    { value: 1024,      suffix: 'KB', digits: 1 },
];

export const fmtPkts = (n: number): string => {
    if (!isFinite(n)) return '0';
    for (const { value, suffix, digits } of PKT_TIERS) {
        if (n >= value) return `${(n / value).toFixed(digits)}${suffix}`;
    }
    return Math.round(n).toString();
};

export const fmtBytes = (n: number): string => {
    if (!isFinite(n) || n < 0) return '0 B';
    for (const { value, suffix, digits } of BYTE_TIERS) {
        if (n >= value) return `${(n / value).toFixed(digits)} ${suffix}`;
    }
    return `${Math.round(n)} B`;
};
