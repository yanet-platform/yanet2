/** Derives a base URL from a raw address string entered by the user. */
export const deriveBaseUrl = (addr: string): string => {
    const trimmed = addr.trim();
    if (!trimmed) {
        return '';
    }
    if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
        return trimmed;
    }
    return `http://${trimmed}`;
};
