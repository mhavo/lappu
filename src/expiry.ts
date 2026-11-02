// Share expiry helpers. Must not import "obsidian" so it stays unit-testable.

export type ExpiryOption = "never" | "day" | "week" | "month";

export const EXPIRY_OPTIONS: readonly ExpiryOption[] = ["never", "day", "week", "month"];

export const DEFAULT_EXPIRY: ExpiryOption = "never";

const SECONDS: Record<Exclude<ExpiryOption, "never">, number> = {
    day: 86400,
    week: 7 * 86400,
    month: 30 * 86400
};

export function isExpiryOption(value: unknown): value is ExpiryOption {
    return typeof value === "string" && (EXPIRY_OPTIONS as readonly string[]).includes(value);
}

/** Lifetime in seconds for the server's `expiresIn`, or undefined for "never". */
export function expiryToSeconds(option: ExpiryOption): number | undefined {
    return option === "never" ? undefined : SECONDS[option];
}

/** JSON body for creating a share; `expiresIn` is omitted for "never". */
export function buildShareBody(content: string, option: ExpiryOption): string {
    let seconds = expiryToSeconds(option);
    return JSON.stringify(seconds === undefined ? {content} : {content, expiresIn: seconds});
}

/** Unix seconds of expiry from a server response, or null when absent or invalid (older servers). */
export function parseExpires(value: unknown): number | null {
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : null;
}

/** Whether an expiry (unix seconds) has passed. Items without one never expire. */
export function isExpired(expires: number | null | undefined, nowMs: number = Date.now()): boolean {
    return typeof expires === "number" && expires * 1000 <= nowMs;
}
