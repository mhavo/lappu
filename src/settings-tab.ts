import {App, PluginSettingTab, SettingGroup} from "obsidian";
import LappuPlugin from "./main";
import {LanguageSetting} from "./i18n";
import {EXPIRY_OPTIONS, ExpiryOption, isExpiryOption} from "./expiry";

export class LappuSettingsTab extends PluginSettingTab {

    plugin: LappuPlugin;

    constructor(app: App, plugin: LappuPlugin) {
        super(app, plugin);
        this.plugin = plugin;
        this.icon = "share";
    }

    display(): void {
        let t = this.plugin.t.bind(this.plugin) as LappuPlugin["t"];
        this.containerEl.empty();
        let group = new SettingGroup(this.containerEl);
        group.addSetting(s => s
            .setName(t("settings.url.name"))
            .setDesc(t("settings.url.desc"))
            .addText(text => {
                text.setPlaceholder("https://lappu.example.com");
                text.setValue(this.plugin.settings.url);
                text.onChange(async v => {
                    this.plugin.settings.url = v.trim();
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.token.name"))
            .setDesc(t("settings.token.desc"))
            .addText(text => {
                text.inputEl.type = "password";
                text.inputEl.autocomplete = "off";
                text.setValue(this.plugin.settings.token);
                text.onChange(async v => {
                    this.plugin.settings.token = v.trim();
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.language.name"))
            .setDesc(t("settings.language.desc"))
            .addDropdown(d => {
                d.addOption("auto", t("settings.language.auto"));
                d.addOption("fi", t("settings.language.fi"));
                d.addOption("en", t("settings.language.en"));
                d.setValue(this.plugin.settings.language);
                d.onChange(async v => {
                    this.plugin.settings.language = v as LanguageSetting;
                    await this.plugin.saveSettings();
                    this.plugin.refreshAllViews();
                    this.display();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.includeProperties.name"))
            .setDesc(t("settings.includeProperties.desc"))
            .addToggle(toggle => {
                toggle.setValue(!this.plugin.settings.stripFrontmatter);
                toggle.onChange(async v => {
                    this.plugin.settings.stripFrontmatter = !v;
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.includeNoteName.name"))
            .setDesc(t("settings.includeNoteName.desc"))
            .addToggle(toggle => {
                toggle.setValue(this.plugin.settings.includeNoteName);
                toggle.onChange(async v => {
                    this.plugin.settings.includeNoteName = v;
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.unshareDeleted.name"))
            .setDesc(t("settings.unshareDeleted.desc"))
            .addToggle(toggle => {
                toggle.setValue(this.plugin.settings.unshareDeletedFiles);
                toggle.onChange(async v => {
                    this.plugin.settings.unshareDeletedFiles = v;
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.autoUpdate.name"))
            .setDesc(t("settings.autoUpdate.desc"))
            .addToggle(toggle => {
                toggle.setValue(this.plugin.settings.autoUpdateShares);
                toggle.onChange(async v => {
                    this.plugin.settings.autoUpdateShares = v;
                    await this.plugin.saveSettings();
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.defaultExpiry.name"))
            .setDesc(t("settings.defaultExpiry.desc"))
            .addDropdown(d => {
                for (let option of EXPIRY_OPTIONS)
                    d.addOption(option, t(`expiry.${option}`));
                d.setValue(this.plugin.settings.defaultExpiry);
                d.onChange(async v => {
                    if (isExpiryOption(v)) {
                        this.plugin.settings.defaultExpiry = v as ExpiryOption;
                        await this.plugin.saveSettings();
                    }
                });
            }));
        group.addSetting(s => s
            .setName(t("settings.import.name"))
            .setDesc(t("settings.import.desc"))
            .addButton(b => b
                .setButtonText(t("import.button"))
                .onClick(async () => {
                    if (await this.plugin.importFromPreviousPlugins())
                        this.display();
                })));
    }
}
