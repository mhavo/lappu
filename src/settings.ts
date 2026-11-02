import type {LanguageSetting} from "./i18n";
import type {ExpiryOption} from "./expiry";

export const defaultSettings: LappuSettings = {
    url: "",
    token: "",
    language: "auto",
    shared: [],
    stripFrontmatter: true,
    includeNoteName: true,
    unshareDeletedFiles: true,
    autoUpdateShares: false,
    defaultExpiry: "never",
    defaultEncrypt: false
};

export interface LappuSettings {

    url: string;
    token: string;
    language: LanguageSetting;
    shared: SharedItem[];
    stripFrontmatter: boolean;
    includeNoteName: boolean;
    unshareDeletedFiles: boolean;
    autoUpdateShares: boolean;
    defaultExpiry: ExpiryOption;
    defaultEncrypt: boolean;

}

export interface SharedItem {

    id: string;
    password: string;
    path: string;
    /** Expiry as unix seconds, or null/absent when the share never expires. */
    expires?: number | null;
    /** Whether the content is stored encrypted on the server. Absent on older items (not encrypted). */
    encrypted?: boolean;
    /** Password the content is encrypted with; kept so updates can re-encrypt and the link can be rebuilt. */
    encryptionPassword?: string;
    /** Whether share links carry the encryption password in their fragment. */
    passwordInLink?: boolean;
    /** Encrypted shares only: sha256 of each image's plaintext to the attachment name uploaded for it. */
    attachmentCache?: Record<string, string>;

}

/**
 * Validates one shared item read from untrusted data (data.json or another plugin's data) and
 * returns a copy with only well-typed fields, or null when id, password or path are unusable.
 * An item that claims to be encrypted stays encrypted even if its encryption password is
 * missing or invalid (the password is then dropped), so it can never silently become plain.
 */
export function sanitizeSharedItem(value: unknown): SharedItem | null {
    if (!value || typeof value !== "object")
        return null;
    let raw = value as Record<string, unknown>;
    if (typeof raw.id !== "string" || raw.id === "" || typeof raw.password !== "string" || typeof raw.path !== "string")
        return null;
    let item: SharedItem = {id: raw.id, password: raw.password, path: raw.path};
    if (raw.expires === null || (typeof raw.expires === "number" && Number.isFinite(raw.expires)))
        item.expires = raw.expires as number | null;
    if (raw.encrypted)
        item.encrypted = true;
    if (typeof raw.encryptionPassword === "string" && raw.encryptionPassword !== "")
        item.encryptionPassword = raw.encryptionPassword;
    if (typeof raw.passwordInLink === "boolean")
        item.passwordInLink = raw.passwordInLink;
    let cache = raw.attachmentCache;
    if (cache && typeof cache === "object" && !Array.isArray(cache)) {
        let clean: Record<string, string> = {};
        for (let [key, name] of Object.entries(cache)) {
            if (typeof name === "string")
                clean[key] = name;
        }
        if (Object.keys(clean).length)
            item.attachmentCache = clean;
    }
    return item;
}
