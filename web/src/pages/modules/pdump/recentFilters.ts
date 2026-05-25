const STORAGE_KEY = 'yanet.pdump.recentFilters';
const MAX_RECENT = 6;

export const loadRecentFilters = (): string[] => {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (raw === null) {
            return [];
        }
        const parsed: unknown = JSON.parse(raw);
        if (!Array.isArray(parsed)) {
            return [];
        }
        const valid = parsed.filter(
            (entry): entry is string => typeof entry === 'string' && entry.trim().length > 0
        );
        return valid.slice(0, MAX_RECENT);
    } catch {
        return [];
    }
};

export const pushRecentFilter = (filter: string): void => {
    const trimmed = filter.trim();
    if (trimmed.length === 0) {
        return;
    }
    try {
        const current = loadRecentFilters();
        const without = current.filter(entry => entry !== trimmed);
        const updated = [trimmed, ...without].slice(0, MAX_RECENT);
        localStorage.setItem(STORAGE_KEY, JSON.stringify(updated));
    } catch {
        // Silently ignore storage errors.
    }
};
