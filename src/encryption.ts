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

/** An empty (or whitespace-only) password is rejected; anything else is accepted as typed. */
export function isValidPassword(password: string): boolean {
    return password.trim().length > 0;
}

/**
 * Link fragment key. `-` and `~` are escaped on top of encodeURIComponent because the viewer
 * splits the fragment at `~` and `-` is reserved for its id/anchor separator.
 */
export function encodeLinkKey(password: string): string {
    return encodeURIComponent(password).replace(/-/g, "%2D").replace(/~/g, "%7E");
}

/** Share link: `<base>/#<id>~<key>` when the password is included, else `<base>/#<id>`. */
export function buildShareLink(base: string, id: string, password?: string): string {
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
