/** Returns true if any field in the errors object has a truthy value. */
export const rowHasError = <E extends object>(errs: E): boolean =>
    Object.values(errs).some(Boolean);

/** Count rows that fail validation according to the provided validate function. */
export const countInvalidRows = <T, E extends object>(
    rows: T[],
    validate: (row: T) => E,
): number => rows.filter((row) => rowHasError(validate(row))).length;
