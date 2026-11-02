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

}
