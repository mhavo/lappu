// Pure helpers for encrypted shares: password generation and share links. Must not import "obsidian".
import type {ExpiryOption} from "./expiry";

const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

/** 22 base62 characters carry about 131 bits, i.e. at least 128. */
export const GENERATED_PASSWORD_LENGTH = 22;

/** Random base62 password. Rejection sampling avoids modulo bias (248 = 4 * 62). */
export function generatePassword(length: number = GENERATED_PASSWORD_LENGTH): string {
    let out = "";
    while (out.length < length) {
        for (let byte of crypto.getRandomValues(new Uint8Array(length * 2))) {
            if (byte < 248 && out.length < length)
                out += BASE62[byte % 62];
        }
    }
    return out;
}

/** Whether the string has no lone UTF-16 surrogates (encodeURIComponent throws on those). */
export function isWellFormed(text: string): boolean {
    for (let i = 0; i < text.length; i++) {
        let c = text.charCodeAt(i);
        if (c >= 0xD800 && c <= 0xDBFF) {
            let next = text.charCodeAt(i + 1);
            if (!(next >= 0xDC00 && next <= 0xDFFF))
                return false;
            i++;
        } else if (c >= 0xDC00 && c <= 0xDFFF)
            return false;
    }
    return true;
}

/** An empty (or whitespace-only) password or one with lone surrogates is rejected; anything else is accepted as typed. */
export function isValidPassword(password: string): boolean {
    return password.trim().length > 0 && isWellFormed(password);
}

/** Passwords shorter than this get a warning (not a block); generated ones are far longer. */
export const MIN_RECOMMENDED_PASSWORD_LENGTH = 12;

export function isShortPassword(password: string): boolean {
    return password.length < MIN_RECOMMENDED_PASSWORD_LENGTH;
}

/**
 * Link fragment key. `-` and `~` are escaped on top of encodeURIComponent because the viewer
 * splits the fragment at `~` and `-` is reserved for its id/anchor separator.
 */
export function encodeLinkKey(password: string): string {
    return encodeURIComponent(password).replace(/-/g, "%2D").replace(/~/g, "%7E");
}

/**
 * Share link. Plain shares: `<base>/s/<id>`. Encrypted shares keep the id in the fragment:
 * `<base>/#<id>~<key>` when the password is included, else `<base>/#<id>`.
 */
export function buildShareLink(base: string, id: string, encrypted: boolean, password?: string): string {
    if (!encrypted)
        return `${base}/s/${encodeURIComponent(id)}`;
    return `${base}/#${id}${password === undefined ? "" : `~${encodeLinkKey(password)}`}`;
}

/** Whether a server response confirms the share is stored as encrypted (older servers do not echo it). */
export function confirmsEncryption(json: unknown): boolean {
    return typeof json === "object" && json !== null && (json as { encrypted?: unknown }).encrypted === true;
}

/** Options chosen in the share dialog. `password` is set exactly when `encrypt` is true. */
export interface ShareOptions {
    expiry: ExpiryOption;
    encrypt: boolean;
    password?: string;
    passwordInLink: boolean;
}
