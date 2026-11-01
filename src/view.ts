import {ButtonComponent, ItemView, TFile, WorkspaceLeaf} from "obsidian";
import LappuPlugin, {removeExtension} from "./main";

export class LappuView extends ItemView {

    public static readonly type: string = "lappu-view";

    private readonly plugin: LappuPlugin;

    constructor(plugin: LappuPlugin, leaf: WorkspaceLeaf) {
        super(leaf);
        this.plugin = plugin;
    }

    public refresh(): void {
        this.contentEl.empty();
        let content = this.contentEl.createDiv({cls: "lappu-view"});
        if (this.plugin.settings.shared.length > 0) {
            for (let shared of [...this.plugin.settings.shared].reverse()) {
                let file = this.plugin.app.vault.getAbstractFileByPath(shared.path) as TFile;
                let div = content.createDiv({cls: "lappu-shared-item"});
                div.createSpan({cls: "lappu-shared-name", text: removeExtension(shared.path).split(/[/\\]/g).pop()});
                if (shared.path.match(/[/\\]/))
                    div.createSpan({cls: "lappu-shared-path", text: removeExtension(shared.path)});
                if (!file)
                    div.createEl("em", {cls: "lappu-shared-path", text: this.plugin.t("view.fileMissing")});
                new ButtonComponent(div)
                    .setClass("clickable-icon")
                    .setTooltip(this.plugin.t("action.copy"))
                    .setIcon("link")
                    .onClick(async () => this.plugin.copyShareLink(shared));
                if (file) {
                    new ButtonComponent(div)
                        .setClass("clickable-icon")
                        .setTooltip(this.plugin.t("action.open"))
                        .setIcon("edit")
                        .onClick(async e => {
                            let leaf = this.app.workspace.getLeaf(e.ctrlKey);
                            await leaf.openFile(file);
                            this.app.workspace.setActiveLeaf(leaf, {focus: true});
                        });
                    new ButtonComponent(div)
                        .setClass("clickable-icon")
                        .setTooltip(this.plugin.t("action.update"))
                        .setIcon("share")
                        .onClick(async () => this.plugin.updateFile(shared, file));
                }
                new ButtonComponent(div)
                    .setClass("clickable-icon")
                    .setTooltip(this.plugin.t("action.delete"))
                    .setIcon("trash")
                    .onClick(async () => this.plugin.deleteFile(shared));
            }
        } else {
            content.createSpan({text: this.plugin.t("view.empty")});
        }
    }

    public onload(): void {
        this.refresh();
    }

    public getDisplayText(): string {
        return this.plugin.t("view.title");
    }

    public getViewType(): string {
        return LappuView.type;
    }

    public getIcon(): string {
        return "share";
    }

}
