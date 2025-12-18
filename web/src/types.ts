export const PAGE_IDS = ['inspect', 'functions', 'pipelines', 'devices', 'neighbours', 'route', 'decap', 'pdump'] as const;

export type PageId = typeof PAGE_IDS[number];
