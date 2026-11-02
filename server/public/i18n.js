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
            expires: "Expires: {date}",
            language: "Language",
            encryptedNote: "This note is encrypted",
            password: "Password",
            openNote: "Open",
            wrongPassword: "Wrong password",
            decrypting: "Decrypting...",
            invalidEncrypted: "The encrypted note is damaged or in an unknown format.",
            cryptoUnavailable: "Decryption needs a secure connection (https) and a browser with WebAssembly support.",
            decryptingImages: "Decrypting images {done}/{total}...",
            imageFailed: "The image could not be loaded.",
            copy: "Copy",
            copyOptions: "Copy as…",
            copyMarkdown: "Markdown",
            copyPlainText: "Plain text",
            copyHtml: "HTML",
            copied: "Copied",
            copyFailed: "The browser did not allow copying",
            copyHtmlUnsupported: "This browser can't copy as HTML"
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
            expires: "Vanhenee: {date}",
            language: "Kieli",
            encryptedNote: "Tämä lappu on salattu",
            password: "Salasana",
            openNote: "Avaa",
            wrongPassword: "Väärä salasana",
            decrypting: "Puretaan salausta...",
            invalidEncrypted: "Salattu lappu on vioittunut tai tuntemattomassa muodossa.",
            cryptoUnavailable: "Salauksen purku vaatii suojatun yhteyden (https) ja WebAssembly-tuen selaimelta.",
            decryptingImages: "Puretaan kuvien salausta {done}/{total}...",
            imageFailed: "Kuvaa ei voitu ladata.",
            copy: "Kopioi",
            copyOptions: "Kopioi muodossa…",
            copyMarkdown: "Markdown",
            copyPlainText: "Pelkkä teksti",
            copyHtml: "HTML",
            copied: "Kopioitu",
            copyFailed: "Selain ei sallinut kopiointia",
            copyHtmlUnsupported: "Tämä selain ei osaa kopioida HTML-muodossa"
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
