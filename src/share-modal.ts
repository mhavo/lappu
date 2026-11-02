import {App, Modal, Notice, Setting} from "obsidian";
import {EXPIRY_OPTIONS, ExpiryOption} from "./expiry";
import {generatePassword, isValidPassword, ShareOptions} from "./encryption";
import type LappuPlugin from "./main";

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

        new Setting(contentEl)
            .setName(t("share.expiry"))
            .addDropdown(d => {
                for (let option of EXPIRY_OPTIONS)
                    d.addOption(option, t(`expiry.${option}`));
                d.setValue(this.expiry);
                d.onChange(v => this.expiry = v as ExpiryOption);
            });

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
            new Setting(contentEl)
                .setName(t("share.password"))
                .addText(text => {
                    text.inputEl.addClass("lappu-password-input");
                    text.inputEl.autocomplete = "off";
                    text.setValue(this.password);
                    text.onChange(v => this.password = v);
                });
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
            new Notice(this.plugin.t("share.password.empty"));
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
