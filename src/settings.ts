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
    defaultExpiry: "never"
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

}

export interface SharedItem {

    id: string;
    password: string;
    path: string;
    /** Expiry as unix seconds, or null/absent when the share never expires. */
    expires?: number | null;

}
