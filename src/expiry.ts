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

/** JSON body for creating a share; `expiresIn` is omitted for "never", `encrypted` unless false, `attachments` when empty. */
export function buildShareBody(content: string, option: ExpiryOption, encrypted: boolean = false, attachments: string[] = []): string {
    let seconds = expiryToSeconds(option);
    let body: Record<string, unknown> = {content};
    if (seconds !== undefined)
        body.expiresIn = seconds;
    if (encrypted)
        body.encrypted = true;
    if (attachments.length)
        body.attachments = [...new Set(attachments)];
    return JSON.stringify(body);
}

/** JSON body for updating a share. */
export function buildUpdateBody(content: string, encrypted: boolean = false, attachments?: string[]): string {
    let body: Record<string, unknown> = encrypted ? {content, encrypted: true} : {content};
    // The full current set, also when empty, so the server prunes the rest.
    if (attachments)
        body.attachments = [...new Set(attachments)];
    return JSON.stringify(body);
}

/** Unix seconds of expiry from a server response, or null when absent or invalid (older servers). */
export function parseExpires(value: unknown): number | null {
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : null;
}

/** Whether an expiry (unix seconds) has passed. Items without one never expire. */
export function isExpired(expires: number | null | undefined, nowMs: number = Date.now()): boolean {
    return typeof expires === "number" && expires * 1000 <= nowMs;
}

/** JSON body that only changes a share's expiry; "never" is sent as null. */
export function buildExpiryBody(option: ExpiryOption): string {
    return JSON.stringify({expiresIn: expiryToSeconds(option) ?? null});
}

/**
 * New expiry (unix seconds, or null for never) from the reply to an expiry-only PATCH, or
 * undefined when the reply has no valid `expires` key, i.e. the server did not apply it (older servers).
 */
export function parseExpiryReply(json: unknown): number | null | undefined {
    if (typeof json !== "object" || json === null || !("expires" in json))
        return undefined;
    let raw = (json as { expires: unknown }).expires;
    let expires = parseExpires(raw);
    return raw !== null && expires === null ? undefined : expires;
}

/** Whether the expiry the server reports is the kind that was asked for (a time, or never). */
export function expiryMatches(option: ExpiryOption, expires: number | null): boolean {
    return (option === "never") === (expires === null);
}

/** Splits shares into those whose stored expiry has passed and the rest (to be checked on the server). */
export function partitionExpired<T extends { expires?: number | null }>(items: readonly T[], nowMs: number = Date.now()): { expired: T[], remaining: T[] } {
    let expired: T[] = [];
    let remaining: T[] = [];
    for (let item of items)
        (isExpired(item.expires, nowMs) ? expired : remaining).push(item);
    return {expired, remaining};
}

/**
 * Whether a meta lookup shows the share is gone: a 404 with the server's JSON error body.
 * Network errors, other statuses and non-JSON 404s (e.g. a wrong server URL) keep the item.
 */
export function isGoneReply(status: number, json: unknown): boolean {
    return status === 404 && typeof json === "object" && json !== null && !Array.isArray(json) && "error" in json;
}
