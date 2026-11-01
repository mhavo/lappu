// UI translations for the viewer. One table per language; to add a language,
// add a table with the same keys. Works in the browser (globalThis.LappuI18n)
// and in Node (module.exports) for tests.
(function (root) {
    "use strict";

    const translations = {
        en: {
            loading: "Loading...",
            errorLoading: "Error loading shared note with id {id}: {details}",
            notFound: "No shared note found with id {id}.",
            home: "Home",
            createdUsing: "Created using {app} for {obsidian}",
            downloadMarkdown: "Download Markdown",
            copyToObsidian: "Copy to Obsidian",
            shared: "Shared: {date}",
            language: "Language"
        },
        fi: {
            loading: "Ladataan...",
            errorLoading: "Virhe ladattaessa jakoa tunnuksella {id}: {details}",
            notFound: "Jakoa tunnuksella {id} ei löytynyt.",
            home: "Etusivu",
            createdUsing: "Tehty {obsidian}-sovelluksen {app} -lisäosalla",
            downloadMarkdown: "Lataa Markdown",
            copyToObsidian: "Kopioi Obsidianiin",
            shared: "Jaettu: {date}",
            language: "Kieli"
        }
    };
    const languages = Object.keys(translations);
    const defaultLanguage = "en";

    function isSupported(lang) {
        return typeof lang === "string" && Object.prototype.hasOwnProperty.call(translations, lang);
    }

    // A stored, supported choice wins; otherwise fi* browsers get Finnish and
    // everyone else English.
    function resolveLanguage(stored, navigatorLanguage) {
        if (isSupported(stored))
            return stored;
        const nav = typeof navigatorLanguage === "string" ? navigatorLanguage.toLowerCase() : "";
        if (nav === "fi" || nav.startsWith("fi-"))
            return "fi";
        return defaultLanguage;
    }

    function translate(lang, key) {
        const table = isSupported(lang) ? translations[lang] : translations[defaultLanguage];
        if (Object.prototype.hasOwnProperty.call(table, key))
            return table[key];
        if (Object.prototype.hasOwnProperty.call(translations[defaultLanguage], key))
            return translations[defaultLanguage][key];
        return key;
    }

    const api = { translations, languages, defaultLanguage, resolveLanguage, translate };
    if (typeof module === "object" && module.exports)
        module.exports = api;
    else
        root.LappuI18n = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
