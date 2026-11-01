import type {LanguageSetting} from "./i18n";

export const defaultSettings: LappuSettings = {
    url: "",
    token: "",
    language: "auto",
    shared: [],
    stripFrontmatter: true,
    includeNoteName: true,
    unshareDeletedFiles: true,
    autoUpdateShares: false
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

}

export interface SharedItem {

    id: string;
    password: string;
    path: string;

}
