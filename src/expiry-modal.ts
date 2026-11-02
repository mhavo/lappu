import {App, SuggestModal} from "obsidian";
import {EXPIRY_OPTIONS, ExpiryOption} from "./expiry";
import type LappuPlugin from "./main";

/** Small picker for a new share's expiry; the default option is listed first, so Enter accepts it. */
export class ExpiryModal extends SuggestModal<ExpiryOption> {

    private chosen = false;

    constructor(app: App, private readonly plugin: LappuPlugin, private readonly onChoose: (option: ExpiryOption | undefined) => void) {
        super(app);
        this.setPlaceholder(plugin.t("expiry.prompt"));
    }

    getSuggestions(query: string): ExpiryOption[] {
        let def = this.plugin.settings.defaultExpiry;
        let options = [def, ...EXPIRY_OPTIONS.filter(o => o !== def)];
        let q = query.trim().toLowerCase();
        return options.filter(o => this.plugin.t(`expiry.${o}`).toLowerCase().includes(q));
    }

    renderSuggestion(option: ExpiryOption, el: HTMLElement): void {
        let text = this.plugin.t(`expiry.${option}`);
        if (option === this.plugin.settings.defaultExpiry)
            text += ` (${this.plugin.t("expiry.default")})`;
        el.setText(text);
    }

    onChooseSuggestion(option: ExpiryOption): void {
        this.chosen = true;
        this.onChoose(option);
    }

    onClose(): void {
        super.onClose();
        // onChooseSuggestion runs after close in some versions; defer the cancel check.
        setTimeout(() => {
            if (!this.chosen)
                this.onChoose(undefined);
        }, 0);
    }
}
