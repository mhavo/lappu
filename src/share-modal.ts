import {App, Modal, Notice, Setting} from "obsidian";
import {EXPIRY_OPTIONS, ExpiryOption} from "./expiry";
import {generatePassword, isShortPassword, isValidPassword, ShareOptions} from "./encryption";
import type LappuPlugin from "./main";

/** The expiry choice shared by the share dialog and the change-expiry dialog. */
function addExpiryDropdown(setting: Setting, plugin: LappuPlugin, value: ExpiryOption, onChange: (value: ExpiryOption) => void): Setting {
    return setting
        .setName(plugin.t("share.expiry"))
        .addDropdown(d => {
            for (let option of EXPIRY_OPTIONS)
                d.addOption(option, plugin.t(`expiry.${option}`));
            d.setValue(value);
            d.onChange(v => onChange(v as ExpiryOption));
        });
}

/**
 * Options for a new share: expiry, optional encryption and whether the link carries the password.
 * Enter submits, Esc/close cancels. The callback gets undefined on cancel.
 */
export class ShareModal extends Modal {

    private submitted = false;
    private expiry: ExpiryOption;
    private encrypt: boolean;
    private password = generatePassword();
    private passwordInLink = true;

    constructor(app: App, private readonly plugin: LappuPlugin, private readonly onDone: (options: ShareOptions | undefined) => void) {
        super(app);
        this.expiry = plugin.settings.defaultExpiry;
        this.encrypt = plugin.settings.defaultEncrypt;
        this.scope.register([], "Enter", e => {
            e.preventDefault();
            this.submit();
            return false;
        });
    }

    onOpen(): void {
        this.modalEl.addClass("lappu-share-modal");
        this.setTitle(this.plugin.t("share.title"));
        this.render();
    }

    private render(): void {
        let t = this.plugin.t.bind(this.plugin) as LappuPlugin["t"];
        let {contentEl} = this;
        contentEl.empty();

        addExpiryDropdown(new Setting(contentEl), this.plugin, this.expiry, v => this.expiry = v);

        new Setting(contentEl)
            .setName(t("share.encrypt"))
            .setDesc(t("share.encrypt.desc"))
            .addToggle(toggle => {
                toggle.setValue(this.encrypt);
                toggle.onChange(v => {
                    this.encrypt = v;
                    this.render();
                });
            });

        if (this.encrypt) {
            let warning: HTMLElement;
            let updateWarning = () => warning.setText(isShortPassword(this.password) ? t("share.password.short") : "");
            new Setting(contentEl)
                .setName(t("share.password"))
                .addText(text => {
                    text.inputEl.addClass("lappu-password-input");
                    text.inputEl.autocomplete = "off";
                    text.setValue(this.password);
                    text.onChange(v => {
                        this.password = v;
                        updateWarning();
                    });
                });
            warning = contentEl.createDiv({cls: "lappu-password-warning"});
            warning.style.color = "var(--text-warning)";
            updateWarning();
            new Setting(contentEl)
                .setName(t("share.passwordInLink"))
                .setDesc(t("share.passwordInLink.desc"))
                .addToggle(toggle => {
                    toggle.setValue(this.passwordInLink);
                    toggle.onChange(v => this.passwordInLink = v);
                });
        }

        new Setting(contentEl)
            .addButton(b => b.setButtonText(t("share.submit")).setCta().onClick(() => this.submit()))
            .addButton(b => b.setButtonText(t("share.cancel")).onClick(() => this.close()));
    }

    private submit(): void {
        if (this.encrypt && !isValidPassword(this.password)) {
            new Notice(this.plugin.t(this.password.trim().length > 0 ? "share.password.invalid" : "share.password.empty"));
            return;
        }
        this.submitted = true;
        this.onDone({
            expiry: this.expiry,
            encrypt: this.encrypt,
            password: this.encrypt ? this.password : undefined,
            passwordInLink: this.encrypt && this.passwordInLink
        });
        this.close();
    }

    onClose(): void {
        super.onClose();
        this.contentEl.empty();
        if (!this.submitted)
            this.onDone(undefined);
    }
}

/**
 * New expiry for an existing share, with the same choices as when sharing.
 * Enter submits, Esc/close cancels. The callback gets undefined on cancel.
 */
export class ExpiryModal extends Modal {

    private submitted = false;
    private expiry: ExpiryOption;

    constructor(app: App, private readonly plugin: LappuPlugin, private readonly current: string, private readonly onDone: (expiry: ExpiryOption | undefined) => void) {
        super(app);
        this.expiry = plugin.settings.defaultExpiry;
        this.scope.register([], "Enter", e => {
            e.preventDefault();
            this.submit();
            return false;
        });
    }

    onOpen(): void {
        let t = this.plugin.t.bind(this.plugin) as LappuPlugin["t"];
        this.modalEl.addClass("lappu-expiry-modal");
        this.setTitle(t("expiryModal.title"));
        addExpiryDropdown(new Setting(this.contentEl), this.plugin, this.expiry, v => this.expiry = v)
            .setDesc(t("expiryModal.current", {current: this.current}));
        new Setting(this.contentEl)
            .addButton(b => b.setButtonText(t("expiryModal.submit")).setCta().onClick(() => this.submit()))
            .addButton(b => b.setButtonText(t("share.cancel")).onClick(() => this.close()));
    }

    private submit(): void {
        this.submitted = true;
        this.onDone(this.expiry);
        this.close();
    }

    onClose(): void {
        super.onClose();
        this.contentEl.empty();
        if (!this.submitted)
            this.onDone(undefined);
    }
}
