// Merging data from earlier plugins (see IMPORT_SOURCES). Must not import "obsidian".
import {LappuSettings, SharedItem} from "./settings";

export interface ImportSource {
    /** Plugin id; its data is read from `<configDir>/plugins/<id>/data.json`. */
    id: string;
    /**
     * Whether the token and language may be taken from this source.
     *
     * The previous self-hosted install (`just-share-please-selfhosted`) is this same plugin
     * under its old id, used by the same person against the same server, so its token is the
     * one the user would have to re-enter anyway. It is only taken when no token is set yet
     * and the resulting url is that same source's url, so the token is never sent to a server
     * it was not configured for.
     * The original upstream plugin (`just-share-please`) has no upload token concept and is a
     * different plugin, so token and language are never taken from it, and its url is only
     * taken while no token is set (otherwise an existing token would go to its server).
     */
    trusted: boolean;
}

/** Plugins whose data.json can be imported, in merge order: the previous self-hosted id first. */
export const IMPORT_SOURCES: readonly ImportSource[] = [
    {id: "just-share-please-selfhosted", trusted: true},
    {id: "just-share-please", trusted: false}
];

export interface MergeOptions {
    /** Also take token and language (only when not yet set). See ImportSource.trusted. */
    trusted?: boolean;
}

/** The original plugin's public server; shares there can't be managed by a self-hosted server. */
const ORIGINAL_PUBLIC_URL = /^https?:\/\/jsp\.ellpeck\.de\/?$/i;

const BOOLEAN_KEYS = ["stripFrontmatter", "includeNoteName", "unshareDeletedFiles", "autoUpdateShares"] as const;

export interface ImportResult {
    settings: LappuSettings;
    /** Number of shares that were not present before. */
    added: number;
}

function isSharedItem(value: unknown): value is SharedItem {
    let item = value as SharedItem;
    return !!item && typeof item === "object"
        && typeof item.id === "string" && item.id !== ""
        && typeof item.password === "string"
        && typeof item.path === "string";
}

/**
 * Merges an earlier plugin's data into this plugin's settings without mutating either.
 * Shares are merged and deduplicated by id (existing entries win). The url is only taken
 * when none is configured yet, never the original public server, and from an untrusted
 * source only while no token is set. Token and language are only taken from a trusted
 * source, and only when the token is empty / the language is still "auto". The token is
 * additionally only taken when the url after merging equals this source's url, so url and
 * token always come as a pair from the same file.
 */
export function mergeImported(current: LappuSettings, imported: unknown, options: MergeOptions = {}): ImportResult {
    let settings: LappuSettings = {...current, shared: [...current.shared]};
    if (!imported || typeof imported !== "object")
        return {settings, added: 0};
    let data = imported as Record<string, unknown>;

    let url = typeof data.url === "string" ? data.url.trim() : "";
    let usableUrl = url !== "" && !ORIGINAL_PUBLIC_URL.test(url);
    if (!settings.url.trim() && usableUrl && (options.trusted || !settings.token.trim()))
        settings.url = url;

    if (options.trusted) {
        if (!settings.token.trim() && usableUrl && settings.url.trim() === url
            && typeof data.token === "string" && data.token.trim())
            settings.token = data.token.trim();
        if (settings.language === "auto" && (data.language === "fi" || data.language === "en"))
            settings.language = data.language;
    }

    for (let key of BOOLEAN_KEYS) {
        if (typeof data[key] === "boolean")
            settings[key] = data[key] as boolean;
    }

    let added = 0;
    if (Array.isArray(data.shared)) {
        let ids = new Set(settings.shared.map(s => s.id));
        for (let item of data.shared) {
            if (!isSharedItem(item) || ids.has(item.id))
                continue;
            settings.shared.push({id: item.id, password: item.password, path: item.path});
            ids.add(item.id);
            added++;
        }
    }
    return {settings, added};
}
