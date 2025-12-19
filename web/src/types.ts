export const PAGE_IDS = ['inspect', 'functions', 'pipelines', 'devices', 'neighbours', 'route', 'decap', 'pdump', 'acl'] as const;

export type PageId = typeof PAGE_IDS[number];
