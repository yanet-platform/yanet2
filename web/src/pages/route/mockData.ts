import type { Route } from '../../api/routes';

// Simple seeded random number generator for reproducible data
class SeededRandom {
    private seed: number;

    constructor(seed: number) {
        this.seed = seed;
    }

    next(): number {
        this.seed = (this.seed * 1103515245 + 12345) & 0x7fffffff;
        return this.seed / 0x7fffffff;
    }

    nextInt(min: number, max: number): number {
        return Math.floor(this.next() * (max - min + 1)) + min;
    }
}

// Generate IPv4 prefix
const generateIPv4Prefix = (rng: SeededRandom): string => {
    const octet1 = rng.nextInt(1, 223);
    const octet2 = rng.nextInt(0, 255);
    const octet3 = rng.nextInt(0, 255);
    const octet4 = rng.nextInt(0, 255);
    const prefixLen = rng.nextInt(8, 32);
    return `${octet1}.${octet2}.${octet3}.${octet4}/${prefixLen}`;
};

// Generate IPv6 prefix
const generateIPv6Prefix = (rng: SeededRandom): string => {
    const segments: string[] = [];
    for (let i = 0; i < 8; i++) {
        segments.push(rng.nextInt(0, 0xffff).toString(16));
    }
    const prefixLen = rng.nextInt(32, 128);
    return `${segments.join(':')}/${prefixLen}`;
};

// Generate IPv4 address
const generateIPv4Address = (rng: SeededRandom): string => {
    const octet1 = rng.nextInt(1, 223);
    const octet2 = rng.nextInt(0, 255);
    const octet3 = rng.nextInt(0, 255);
    const octet4 = rng.nextInt(0, 255);
    return `${octet1}.${octet2}.${octet3}.${octet4}`;
};

// Generate IPv6 address
const generateIPv6Address = (rng: SeededRandom): string => {
    const segments: string[] = [];
    for (let i = 0; i < 8; i++) {
        segments.push(rng.nextInt(0, 0xffff).toString(16));
    }
    return segments.join(':');
};

// Route generator that creates routes on-demand
export interface MockRouteGenerator {
    readonly totalCount: number;
    getRoute(index: number): Route;
    getRoutes(startIndex: number, count: number): Route[];
    searchRoutes(query: string, offset: number, limit: number): { routes: Route[]; totalMatched: number };
}

export const createMockRouteGenerator = (totalCount: number, seed: number = 42): MockRouteGenerator => {
    // Cache for frequently accessed routes
    const cache = new Map<number, Route>();
    const CACHE_MAX_SIZE = 10000;

    const generateRoute = (index: number): Route => {
        const rng = new SeededRandom(seed + index * 7919); // Use prime for better distribution

        const isIPv6 = rng.next() > 0.7; // 30% IPv6
        const prefix = isIPv6 ? generateIPv6Prefix(rng) : generateIPv4Prefix(rng);
        const nextHop = isIPv6 ? generateIPv6Address(rng) : generateIPv4Address(rng);
        const peer = isIPv6 ? generateIPv6Address(rng) : generateIPv4Address(rng);

        return {
            prefix,
            nextHop,
            peer,
            routeDistinguisher: rng.nextInt(1, 65535),
            peerAs: rng.nextInt(1, 65535),
            originAs: rng.nextInt(1, 65535),
            med: rng.nextInt(0, 1000),
            pref: rng.nextInt(0, 200),
            asPathLen: rng.nextInt(1, 10),
            source: rng.nextInt(0, 2),
            isBest: rng.next() > 0.5,
        };
    };

    const getRoute = (index: number): Route => {
        if (index < 0 || index >= totalCount) {
            throw new Error(`Index ${index} out of bounds [0, ${totalCount})`);
        }

        const cached = cache.get(index);
        if (cached) {
            return cached;
        }

        const route = generateRoute(index);

        // Limit cache size
        if (cache.size >= CACHE_MAX_SIZE) {
            const keysToDelete = Array.from(cache.keys()).slice(0, CACHE_MAX_SIZE / 2);
            keysToDelete.forEach(key => cache.delete(key));
        }

        cache.set(index, route);
        return route;
    };

    const getRoutes = (startIndex: number, count: number): Route[] => {
        const routes: Route[] = [];
        const endIndex = Math.min(startIndex + count, totalCount);
        for (let i = startIndex; i < endIndex; i++) {
            routes.push(getRoute(i));
        }
        return routes;
    };

    // Search routes - for demonstration, we'll do a simple linear search
    // In production, this would be server-side with proper indexing
    const searchRoutes = (query: string, offset: number, limit: number): { routes: Route[]; totalMatched: number } => {
        if (!query.trim()) {
            return {
                routes: getRoutes(offset, limit),
                totalMatched: totalCount,
            };
        }

        const lowerQuery = query.toLowerCase();
        const matchedRoutes: Route[] = [];
        let matchedCount = 0;
        let skipped = 0;

        // For 20M routes, we need to limit search scope for performance
        // In real implementation, this would be server-side with indexing
        const maxSearchCount = Math.min(totalCount, 1_000_000); // Limit search to 1M for client-side demo

        for (let i = 0; i < maxSearchCount && matchedRoutes.length < limit; i++) {
            const route = getRoute(i);
            const matches =
                route.prefix?.toLowerCase().includes(lowerQuery) ||
                route.nextHop?.toLowerCase().includes(lowerQuery) ||
                route.peer?.toLowerCase().includes(lowerQuery);

            if (matches) {
                matchedCount++;
                if (skipped >= offset) {
                    matchedRoutes.push(route);
                } else {
                    skipped++;
                }
            }
        }

        return {
            routes: matchedRoutes,
            totalMatched: matchedCount,
        };
    };

    return {
        totalCount,
        getRoute,
        getRoutes,
        searchRoutes,
    };
};

// Pre-built mock configurations
export const MOCK_CONFIGS = {
    small: { routeCount: 1_000, label: '1K routes' },
    medium: { routeCount: 100_000, label: '100K routes' },
    large: { routeCount: 1_000_000, label: '1M routes' },
    huge: { routeCount: 20_000_000, label: '20M routes' },
};

// Enable/disable mock mode
let mockEnabled = false;
let mockGenerator: MockRouteGenerator | null = null;

export const enableMockMode = (routeCount: number = MOCK_CONFIGS.huge.routeCount): void => {
    mockEnabled = true;
    mockGenerator = createMockRouteGenerator(routeCount);
    console.log(`Mock mode enabled with ${routeCount.toLocaleString()} routes`);
};

export const disableMockMode = (): void => {
    mockEnabled = false;
    mockGenerator = null;
    console.log('Mock mode disabled');
};

export const isMockEnabled = (): boolean => mockEnabled;

export const getMockGenerator = (): MockRouteGenerator | null => mockGenerator;

