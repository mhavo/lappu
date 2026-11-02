import {ButtonComponent, moment, Notice, Plugin, requestUrl, TFile} from "obsidian";
import {defaultSettings, LappuSettings, SharedItem, sanitizeSharedItem} from "./settings";
import {LappuSettingsTab} from "./settings-tab";
import {LappuView} from "./view";
import {Language, resolveLanguage, translate, TranslationKey} from "./i18n";
import {attachmentEndpoint, baseUrl, buildHeaders, classifyResponse, isConfigured, Operation, parseMissing, ResponseKind, shareEndpoint} from "./http";
import {buildShareBody, buildUpdateBody, DEFAULT_EXPIRY, isExpiryOption, parseExpires} from "./expiry";
import {ShareModal} from "./share-modal";
import {buildShareLink, confirmsEncryption, ShareOptions} from "./encryption";
import {encrypt, encryptBytes} from "./crypter";
import {defaultStore, rewriteAttachments, sha256Hex, Store} from "./attachments";
import {IMPORT_SOURCES, ImportSource, mergeImported} from "./import";

/** A note ready to upload: rewritten markdown plus the image files it references. */
interface PreparedNote {
    markdown: string;
    /** Attachment name to bytes; null bytes are already on the server (encrypted shares, cached). */
    attachments: Map<string, Uint8Array | null>;
    labels: Map<string, string>;
    /** Plaintext sha256 to uploaded name, for encrypted shares. */
    cache: Record<string, string>;
}

interface ServerResponse {
    status: number;
    text: string;
    json: unknown;
}

const ERROR_MESSAGES: Record<Exclude<ResponseKind, "ok" | "other">, TranslationKey> = {
    invalidToken: "error.invalidToken",
    wrongPassword: "error.wrongPassword",
    notFound: "error.notFound",
    tooLarge: "error.tooLarge",
    rateLimited: "error.rateLimited"
};

export default class LappuPlugin extends Plugin {

    public settings: LappuSettings;

    async onload(): Promise<void> {
        let hadData = await this.loadSettings();
        this.addSettingTab(new LappuSettingsTab(this.app, this));

        this.registerView(LappuView.type, l => new LappuView(this, l));
        this.addCommand({
            id: `open-${LappuView.type}`,
            name: this.t("command.openView"),
            callback: async () => {
                if (!this.app.workspace.getLeavesOfType(LappuView.type).length)
                    await this.app.workspace.getRightLeaf(false).setViewState({type: LappuView.type, active: true});
                this.app.workspace.revealLeaf(this.app.workspace.getLeavesOfType(LappuView.type)[0]);
            }
        });

        this.registerEvent(this.app.workspace.on("file-menu", async (m, f) => {
            if (f instanceof TFile && f.extension == "md") {
                let shared = this.getSharedItem(f.path);
                if (!shared) {
                    m.addItem(i => {
                        i.setTitle(this.t("action.share"));
                        i.setIcon("share");
                        i.onClick(async () => this.shareFile(f));
                    });
                } else {
                    m.addItem(i => {
                        i.setTitle(this.t("action.copy"));
                        i.setIcon("link");
                        i.onClick(() => this.copyShareLink(shared));
                    });
                    m.addItem(i => {
                        i.setTitle(this.t("action.update"));
                        i.setIcon("share");
                        i.onClick(() => this.updateFile(shared, f));
                    });
                    m.addItem(i => {
                        i.setTitle(this.t("action.delete"));
                        i.setIcon("trash");
                        i.onClick(async () => this.deleteFile(shared));
                    });
                }
            }
        }));

        this.registerEvent(this.app.vault.on("rename", (f, p) => {
            if (f instanceof TFile) {
                let shared = this.getSharedItem(p);
                if (shared) {
                    shared.path = f.path;
                    this.refreshAllViews();
                }
            }
        }));
        this.registerEvent(this.app.vault.on("delete", f => {
            if (f instanceof TFile) {
                let shared = this.getSharedItem(f.path);
                if (shared) {
                    if (this.settings.unshareDeletedFiles) {
                        this.deleteFile(shared, false);
                    } else {
                        this.refreshAllViews();
                    }
                }
            }
        }));
        this.registerEvent(this.app.vault.on("modify", f => {
            if (this.settings.autoUpdateShares && f instanceof TFile) {
                let shared = this.getSharedItem(f.path);
                if (shared)
                    this.updateFile(shared, f, false);
            }
        }));

        this.addCommand({
            id: "share",
            name: this.t("command.share"),
            editorCheckCallback: (checking, _, ctx) => {
                if (!this.getSharedItem(ctx.file.path)) {
                    if (!checking)
                        this.shareFile(ctx.file);
                    return true;
                }
                return false;
            }
        });
        this.addCommand({
            id: "copy",
            name: this.t("command.copy"),
            editorCheckCallback: (checking, _, ctx) => {
                let shared = this.getSharedItem(ctx.file.path);
                if (shared) {
                    if (!checking)
                        this.copyShareLink(shared);
                    return true;
                }
                return false;
            }
        });
        this.addCommand({
            id: "update",
            name: this.t("command.update"),
            editorCheckCallback: (checking, _, ctx) => {
                let shared = this.getSharedItem(ctx.file.path);
                if (shared) {
                    if (!checking)
                        this.updateFile(shared, ctx.file);
                    return true;
                }
                return false;
            }
        });
        this.addCommand({
            id: "delete",
            name: this.t("command.delete"),
            editorCheckCallback: (checking, _, ctx) => {
                let shared = this.getSharedItem(ctx.file.path);
                if (shared) {
                    if (!checking)
                        this.deleteFile(shared);
                    return true;
                }
                return false;
            }
        });
        this.addCommand({
            id: "import-previous",
            name: this.t("command.import"),
            callback: () => this.importFromPreviousPlugins()
        });

        if (!hadData && (await this.findImportSources()).length)
            this.app.workspace.onLayoutReady(() => this.offerImport());
    }

    /** Loads settings and returns whether this plugin had any saved data. */
    async loadSettings(): Promise<boolean> {
        let data = await this.loadData();
        this.settings = Object.assign({}, defaultSettings, data);
        let shared: unknown = this.settings.shared;
        this.settings.shared = (Array.isArray(shared) ? shared : [])
            .map(sanitizeSharedItem).filter((item): item is SharedItem => item !== null);
        if (!isExpiryOption(this.settings.defaultExpiry))
            this.settings.defaultExpiry = DEFAULT_EXPIRY;
        return !!data;
    }

    async saveSettings(): Promise<void> {
        await this.saveData(this.settings);
    }

    language(): Language {
        let obsidianLanguage: string | null = null;
        try {
            obsidianLanguage = window.localStorage.getItem("language");
        } catch (e) {
            // localStorage may be unavailable; fall back to moment below
        }
        return resolveLanguage(this.settings?.language, obsidianLanguage || moment.locale());
    }

    t(key: TranslationKey, params?: Record<string, string | number>): string {
        return translate(this.language(), key, params);
    }

    getSharedItem(path: string): SharedItem {
        return this.settings.shared.find(f => f.path == path);
    }

    private sourceDataPath(source: ImportSource): string {
        return `${this.app.vault.configDir}/plugins/${source.id}/data.json`;
    }

    /** Import sources (see IMPORT_SOURCES) that have a data.json in this vault, in merge order. */
    async findImportSources(): Promise<ImportSource[]> {
        let found: ImportSource[] = [];
        for (let source of IMPORT_SOURCES) {
            try {
                if (await this.app.vault.adapter.exists(this.sourceDataPath(source)))
                    found.push(source);
            } catch (e) {
                // treat an unreadable location as missing
            }
        }
        return found;
    }

    private offerImport(): void {
        let notice = new Notice(createFragment(f => {
            f.createSpan({text: this.t("import.offer")});
            new ButtonComponent(f.createDiv({cls: "lappu-notice-buttons"}))
                .setButtonText(this.t("import.button"))
                .onClick(async () => {
                    notice.hide();
                    await this.importFromPreviousPlugins();
                });
        }), 0);
    }

    /**
     * Imports url, shares and options from earlier plugins' data.json files, previous
     * self-hosted install first so its url and token win over the original plugin's.
     * mergeImported only takes a token together with the same source's url, and an
     * untrusted url only while no token is set, so a token never pairs with a foreign url.
     */
    async importFromPreviousPlugins(): Promise<boolean> {
        try {
            let sources = await this.findImportSources();
            if (!sources.length) {
                new Notice(this.t("import.notFound"));
                return false;
            }
            let settings = this.settings;
            let added = 0;
            for (let source of sources) {
                let imported = JSON.parse(await this.app.vault.adapter.read(this.sourceDataPath(source)));
                let result = mergeImported(settings, imported, {trusted: source.trusted});
                settings = result.settings;
                added += result.added;
            }
            this.settings = settings;
            await this.saveSettings();
            this.refreshAllViews();
            new Notice(this.t("import.done", {count: added}));
            return true;
        } catch (e) {
            new Notice(this.t("import.failed", {error: String(e)}), 10000);
            console.log(e);
            return false;
        }
    }

    openSettings(): void {
        // Not part of the public API, so guard every step.
        let setting = (this.app as unknown as { setting?: { open(): void, openTabById(id: string): void } }).setting;
        if (setting) {
            setting.open();
            setting.openTabById(this.manifest.id);
        }
    }

    /** Shows a notice and returns false when the server url or token is missing. */
    private ensureConfigured(): boolean {
        if (isConfigured(this.settings))
            return true;
        let notice = new Notice(createFragment(f => {
            f.createSpan({text: this.t("notice.notConfigured")});
            new ButtonComponent(f.createDiv({cls: "lappu-notice-buttons"}))
                .setButtonText(this.t("button.openSettings"))
                .onClick(() => {
                    notice.hide();
                    this.openSettings();
                });
        }), 10000);
        return false;
    }

    private async request(method: string, url: string, headers: Record<string, string>, body?: string | Uint8Array): Promise<ServerResponse> {
        let response = await requestUrl({
            url, method, headers, throw: false,
            body: body instanceof Uint8Array ? body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength) as ArrayBuffer : body,
            contentType: body === undefined ? undefined : body instanceof Uint8Array ? "application/octet-stream" : "application/json"
        });
        let text = "";
        try {
            text = response.text;
        } catch (e) {
            // empty or binary body
        }
        let json: unknown = null;
        try {
            json = JSON.parse(text);
        } catch (e) {
            // not JSON
        }
        return {status: response.status, text, json};
    }

    /** Message describing a failed response, for the given operation. */
    private describeFailure(kind: ResponseKind, status: number): string {
        if (kind === "ok" || kind === "other")
            return this.t("error.other", {status});
        return this.t(ERROR_MESSAGES[kind]);
    }

    private showError(operation: Operation, name: string, detail: string, localItem?: SharedItem, buttonKey?: TranslationKey): void {
        let titleKey: TranslationKey = operation === "share" ? "error.share" : operation === "update" ? "error.update" : "error.delete";
        let notice = new Notice(createFragment(f => {
            f.createSpan({text: `${this.t(titleKey, {name})} `});
            f.createEl("code", {text: detail});
            if (localItem && buttonKey) {
                new ButtonComponent(f.createDiv({cls: "lappu-notice-buttons"}))
                    .setButtonText(this.t(buttonKey))
                    .onClick(async _ => {
                        notice.hide();
                        await this.deleteLocalFileInfo(localItem);
                        new Notice(this.t("notice.localDeleted", {name}));
                    });
            }
        }), 10000);
    }

    /** Asks for the options of a new share; resolves undefined if the user cancels. */
    private chooseShareOptions(): Promise<ShareOptions | undefined> {
        return new Promise(resolve => new ShareModal(this.app, this, resolve).open());
    }

    /** Shares a file. Without explicit options the user is asked, with defaults from the settings. */
    async shareFile(file: TFile, options?: ShareOptions): Promise<SharedItem> {
        if (!this.ensureConfigured())
            return undefined;
        options = options ?? await this.chooseShareOptions();
        if (!options)
            return undefined;
        try {
            let prepared = await this.prepareNote(file, options.encrypt ? options.password : undefined);
            let content = options.encrypt ? await encrypt(options.password, prepared.markdown) : prepared.markdown;
            let response = await this.request("POST", shareEndpoint(this.settings.url), buildHeaders(this.settings.token),
                buildShareBody(content, options.expiry, options.encrypt, [...prepared.attachments.keys()]));
            let kind = classifyResponse(response.status, response.text, "share");
            let data = response.json as Partial<SharedItem>;
            if (kind !== "ok" || !data || typeof data.id !== "string" || typeof data.password !== "string") {
                this.showError("share", file.basename, kind === "ok" ? this.t("error.other", {status: response.status}) : this.describeFailure(kind, response.status));
                return undefined;
            }
            let shared: SharedItem = {id: data.id, password: data.password, path: file.path, expires: parseExpires(data.expires)};
            if (options.encrypt) {
                shared.encrypted = true;
                shared.encryptionPassword = options.password;
                shared.passwordInLink = options.passwordInLink;
                if (Object.keys(prepared.cache).length)
                    shared.attachmentCache = prepared.cache;
            }

            this.settings.shared.push(shared);
            await this.saveSettings();
            this.refreshAllViews();

            await this.uploadAttachments(shared, file, prepared, response.json);
            try {
                await this.copyShareLink(shared, false);
            } catch (e) {
                // The share exists; only the clipboard step failed.
                console.log(e);
                new Notice(this.t("notice.linkCopyFailed", {name: file.basename}), 10000);
            }
            new Notice(this.t("notice.shared", {name: file.basename}));
            // Older servers ignore expiresIn; the user must not assume the share will vanish.
            if (options.expiry !== "never" && shared.expires === null)
                new Notice(this.t("notice.expiryUnsupported", {name: file.basename}), 10000);
            // Older servers do not know `encrypted`; the viewer would show ciphertext.
            if (options.encrypt && !confirmsEncryption(response.json))
                new Notice(this.t("notice.encryptionUnsupported", {name: file.basename}), 10000);
            return shared;
        } catch (e) {
            this.showError("share", file.basename, String(e));
            console.log(e);
        }
    }

    async updateFile(item: SharedItem, file: TFile, notice = true, retry = true): Promise<boolean> {
        if (!this.ensureConfigured())
            return false;
        try {
            let encrypted = !!item.encrypted;
            if (encrypted && (typeof item.encryptionPassword !== "string" || !item.encryptionPassword)) {
                // Never fall back to sending an encrypted share's note as plaintext.
                new Notice(this.t("notice.encryptedNoPassword", {name: file.basename}), 10000);
                return false;
            }
            let prepared = await this.prepareNote(file, encrypted ? item.encryptionPassword : undefined, item.attachmentCache);
            let response = await this.request("PATCH", shareEndpoint(this.settings.url, item.id), buildHeaders(this.settings.token, item.password),
                buildUpdateBody(encrypted ? await encrypt(item.encryptionPassword, prepared.markdown) : prepared.markdown, encrypted,
                    [...prepared.attachments.keys()]));
            let kind = classifyResponse(response.status, response.text, "update");
            if (kind !== "ok") {
                this.showError("update", file.basename, this.describeFailure(kind, response.status),
                    kind === "notFound" ? item : undefined, "button.removeLocal");
                return false;
            }
            // A cached name the server no longer has cannot be re-uploaded (the ciphertext is gone): start over without the cache.
            let missing = parseMissing(response.json) ?? [];
            if (retry && item.attachmentCache && missing.some(n => prepared.attachments.get(n) === null)) {
                delete item.attachmentCache;
                return await this.updateFile(item, file, notice, false);
            }
            if (encrypted) {
                if (Object.keys(prepared.cache).length)
                    item.attachmentCache = prepared.cache;
                else
                    delete item.attachmentCache;
                await this.saveSettings();
            }
            await this.uploadAttachments(item, file, prepared, response.json);
            if (notice)
                new Notice(this.t("notice.updated", {name: file.basename}));
            // An explicit encrypted: false means the server stored ciphertext as plain text.
            if (encrypted && response.json && (response.json as { encrypted?: unknown }).encrypted === false)
                new Notice(this.t("notice.encryptionUnsupported", {name: file.basename}), 10000);
            return true;
        } catch (e) {
            this.showError("update", file.basename, String(e));
            console.log(e);
            return false;
        }
    }

    async deleteFile(item: SharedItem, notice = true): Promise<boolean> {
        let name = removeExtension(item.path);
        if (!this.ensureConfigured())
            return false;
        try {
            let response = await this.request("DELETE", shareEndpoint(this.settings.url, item.id), buildHeaders(this.settings.token, item.password));
            let kind = classifyResponse(response.status, response.text, "delete");
            if (kind !== "ok") {
                this.showError("delete", name, this.describeFailure(kind, response.status), item,
                    kind === "notFound" ? "button.removeLocal" : "button.forceDeleteLocal");
                return false;
            }

            await this.deleteLocalFileInfo(item);

            if (notice)
                new Notice(this.t("notice.deleted", {name}));
            return true;
        } catch (e) {
            this.showError("delete", name, String(e), item, "button.forceDeleteLocal");
            console.log(e);
            return false;
        }
    }

    async deleteLocalFileInfo(item: SharedItem): Promise<void> {
        this.settings.shared.remove(item);
        await this.saveSettings();
        this.refreshAllViews();
    }

    async copyShareLink(item: SharedItem, notice = true): Promise<void> {
        let password = item.encrypted && item.passwordInLink ? item.encryptionPassword : undefined;
        await navigator.clipboard.writeText(buildShareLink(baseUrl(this.settings.url), item.id, password));
        if (notice)
            new Notice(this.t("notice.linkCopied", {name: removeExtension(item.path)}));
    }

    async copyPassword(item: SharedItem): Promise<void> {
        if (!item.encryptionPassword)
            return;
        await navigator.clipboard.writeText(item.encryptionPassword);
        new Notice(this.t("notice.passwordCopied", {name: removeExtension(item.path)}));
    }

    /** Reads the note, rewrites its local images into attachments and reports embeds that stay unshared. */
    async prepareNote(file: TFile, encryptionPassword?: string, cache: Record<string, string> = {}): Promise<PreparedNote> {
        let text = await this.preProcessMarkdown(file);
        let newCache: Record<string, string> = {};
        let store: Store = defaultStore;
        if (encryptionPassword !== undefined) {
            // Ciphertext differs on every run, so reuse the name uploaded for the same plaintext.
            store = async (bytes, ext) => {
                let plain = await sha256Hex(bytes);
                let known = newCache[plain] ?? cache[plain];
                if (known) {
                    newCache[plain] = known;
                    return {name: known, bytes: null};
                }
                let encrypted = await encryptBytes(encryptionPassword, bytes);
                let name = `${await sha256Hex(encrypted)}.${ext}`;
                newCache[plain] = name;
                return {name, bytes: encrypted};
            };
        }
        let result = await rewriteAttachments(text, async link => {
            let dest = this.app.metadataCache.getFirstLinkpathDest(link, file.path);
            if (!dest)
                return null;
            return {bytes: new Uint8Array(await this.app.vault.readBinary(dest)), ext: dest.extension};
        }, store);
        if (result.skipped.length) {
            let shown = result.skipped.slice(0, 5).join(", ") + (result.skipped.length > 5 ? ", …" : "");
            new Notice(this.t("notice.embedsSkipped", {count: result.skipped.length, list: shown}), 10000);
        }
        return {markdown: result.markdown, attachments: result.attachments, labels: result.labels, cache: newCache};
    }

    /** Uploads the attachments the server reports missing and tells the user about failures. */
    private async uploadAttachments(item: SharedItem, file: TFile, prepared: PreparedNote, json: unknown): Promise<void> {
        if (!prepared.attachments.size)
            return;
        let missing = parseMissing(json);
        if (!missing) {
            new Notice(this.t("notice.attachmentsUnsupported", {name: file.basename}), 10000);
            return;
        }
        for (let name of missing) {
            let bytes = prepared.attachments.get(name);
            if (!bytes)
                continue;
            let label = prepared.labels.get(name) ?? name;
            try {
                let response = await this.request("PUT", attachmentEndpoint(this.settings.url, item.id, name),
                    buildHeaders(this.settings.token, item.password), bytes);
                let kind = classifyResponse(response.status, response.text, "update");
                if (kind === "ok")
                    continue;
                if (kind === "tooLarge")
                    new Notice(this.t("error.attachmentTooLarge", {name: label}), 10000);
                else
                    this.showAttachmentError(label, this.describeFailure(kind, response.status));
            } catch (e) {
                this.showAttachmentError(label, String(e));
                console.log(e);
            }
        }
    }

    private showAttachmentError(name: string, detail: string): void {
        new Notice(createFragment(f => {
            f.createSpan({text: `${this.t("error.attachmentFailed", {name})} `});
            f.createEl("code", {text: detail});
        }), 10000);
    }

    async preProcessMarkdown(file: TFile): Promise<string> {
        let text = await this.app.vault.cachedRead(file);

        // strip frontmatter
        let frontmatter = /^(---\s*\n.*?\n---)\s*\n(.*)$/s;
        if (this.settings.stripFrontmatter)
            text = text.replace(frontmatter, "$2");

        // strip comments
        text = text.replace(/%%.*?%%/sg, "");

        // include note name (after frontmatter!)
        if (this.settings.includeNoteName) {
            let title = `# ${file.basename}\n\n`;
            if (frontmatter.test(text)) {
                text = text.replace(frontmatter, `$1\n\n${title}$2`);
            } else {
                text = title + text;
            }
        }

        return text;
    }

    refreshAllViews(): void {
        for (let leaf of this.app.workspace.getLeavesOfType(LappuView.type)) {
            if (leaf.view instanceof LappuView)
                leaf.view.refresh();
        }
    }

}

export function removeExtension(file: string): string {
    let split = file.split(".");
    split.pop();
    return split.join(".");
}
