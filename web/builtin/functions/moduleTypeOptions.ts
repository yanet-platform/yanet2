export interface ModuleTypeSource {
    name?: string | null;
}

export const getAvailableModuleTypesFromInspect = (moduleTypes: ModuleTypeSource[]): string[] => {
    const types = new Set<string>();

    moduleTypes.forEach(moduleType => {
        const name = moduleType.name?.trim() ?? '';
        if (name) {
            types.add(name);
        }
    });

    return [...types].sort((a, b) => a.localeCompare(b));
};
