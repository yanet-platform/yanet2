export const PAGE_IDS = ['inspect', 'functions', 'pipelines', 'devices', 'neighbours', 'route', 'decap'] as const;

export type PageId = typeof PAGE_IDS[number];
