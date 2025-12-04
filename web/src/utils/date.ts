export function getUTCOffsetString(): string {
    const now = new Date();
    const timezoneOffsetMinutes = -now.getTimezoneOffset();
    const offsetHours = Math.floor(Math.abs(timezoneOffsetMinutes) / 60);
    const offsetMinutes = Math.abs(timezoneOffsetMinutes) % 60;
    const offsetSign = timezoneOffsetMinutes >= 0 ? '+' : '-';
    // If minutes are zero, show only hours without leading zero (e.g., "+3" instead of "+03:00")
    if (offsetMinutes === 0) {
        return `${offsetSign}${offsetHours}`;
    }
    return `${offsetSign}${offsetHours}:${offsetMinutes.toString().padStart(2, '0')}`;
}
