export const PAGE_IDS = ['inspect', 'functions', 'pipelines', 'devices', 'neighbours', 'route'] as const;

export type PageId = typeof PAGE_IDS[number];
