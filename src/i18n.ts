// Translation tables and language resolution. Must not import "obsidian" so it stays unit-testable.

export type Language = "fi" | "en";
export type LanguageSetting = "auto" | Language;

const en = {
    "command.openView": "Open Lappu view",
    "command.share": "Share current file as Lappu",
    "command.copy": "Copy current file's Lappu link",
    "command.update": "Update current file's Lappu",
    "command.delete": "Delete current file's Lappu",
    "command.import": "Import shares from Just Share Please",

    "action.share": "Share as Lappu",
    "action.copy": "Copy Lappu link",
    "action.update": "Update Lappu",
    "action.delete": "Delete Lappu",
    "action.open": "Open in Obsidian",
    "action.copyPassword": "Copy password",

    "notice.shared": "Successfully shared {name} and copied link to clipboard",
    "notice.updated": "Successfully updated the Lappu for {name}",
    "notice.deleted": "Successfully deleted the Lappu for {name}",
    "notice.localDeleted": "Successfully deleted local information for {name}",
    "notice.linkCopied": "Copied link to {name} to clipboard",
    "notice.notConfigured": "Set the server URL and token in the Lappu settings first.",
    "notice.expiryUnsupported": "The server did not set an expiry, so {name} stays shared until you delete it.",
    "notice.encryptedNoPassword": "{name} is marked encrypted but its password is missing, so it was not updated (nothing was sent).",
    "notice.linkCopyFailed": "{name} was shared, but copying the link failed. Use the copy link action.",
    "notice.encryptionUnsupported": "The server did not confirm encryption, so {name} may be shown as unreadable text. Update the server.",
    "notice.passwordCopied": "Copied the password of {name} to clipboard",
    "notice.embedsSkipped": "{count} embeds were not shared: {list}",
    "notice.attachmentsUnsupported": "The server does not support attachments, so the images in {name} will not show. Update the server.",

    "button.openSettings": "Open settings",
    "button.forceDeleteLocal": "Force-delete local information",
    "button.removeLocal": "Remove local information",

    "error.share": "There was an error sharing {name}:",
    "error.update": "There was an error updating {name}:",
    "error.delete": "There was an error deleting {name}:",
    "error.invalidToken": "Token missing or wrong – check the settings.",
    "error.wrongPassword": "The server rejected the share password.",
    "error.notFound": "The share no longer exists on the server. You can remove its local information.",
    "error.tooLarge": "The note is too large for the server.",
    "error.attachmentTooLarge": "The image {name} is too large for the server (default limits: 10 MiB per image, 50 MiB per note).",
    "error.attachmentFailed": "Uploading the image {name} failed:",
    "error.rateLimited": "Too many failed attempts. Try again later.",
    "error.other": "The server responded with status {status}.",

    "import.offer": "Shares from an earlier Just Share Please plugin were found.",
    "import.button": "Import shares from Just Share Please",
    "import.done": "Imported settings and {count} new shares from Just Share Please.",
    "import.notFound": "No Just Share Please data was found in this vault.",
    "import.failed": "Importing from Just Share Please failed: {error}",

    "settings.url.name": "Server URL",
    "settings.url.desc": "Address of your self-hosted Lappu server, for example https://lappu.example.com.",
    "settings.token.name": "Token",
    "settings.token.desc": "Upload token configured on the server. It is sent when sharing, updating and deleting.",
    "settings.includeProperties.name": "Include file properties",
    "settings.includeProperties.desc": "Whether the file properties of the shared note should be included in the share as visible frontmatter.",
    "settings.includeNoteName.name": "Include note name",
    "settings.includeNoteName.desc": "Whether the name of the shared note should be included in the share as a heading.",
    "settings.unshareDeleted.name": "Unshare deleted files",
    "settings.unshareDeleted.desc": "Whether shares of files should be removed automatically when they are deleted. Only supported when deleting from within Obsidian.",
    "settings.autoUpdate.name": "Automatically update shares",
    "settings.autoUpdate.desc": "Whether a file's share should automatically be updated when the file is changed from within Obsidian.",
    "settings.defaultExpiry.name": "Default expiry",
    "settings.defaultExpiry.desc": "How long a new share lasts by default. You can choose another time when sharing.",
    "settings.defaultEncrypt.name": "Encrypt new shares by default",
    "settings.defaultEncrypt.desc": "Whether the encrypt option is on by default when sharing. Note: the password of an encrypted share is stored in this plugin's data.json in your vault.",
    "settings.language.name": "Language",
    "settings.language.desc": "Language of this plugin. Command names change after Obsidian is restarted.",
    "settings.language.auto": "Automatic",
    "settings.language.fi": "Suomi",
    "settings.language.en": "English",
    "settings.import.name": "Import from Just Share Please",
    "settings.import.desc": "Copies the shares and options of Just Share Please (Self-hosted) and the original Just Share Please plugin. Existing shares are kept. The server URL, and the token from the self-hosted version, are only taken if none is set yet.",

    "view.title": "Lappu",
    "view.empty": "You have not shared any items yet.",
    "view.fileMissing": "File does not exist anymore",
    "view.expires": "Expires {date}",
    "view.expired": "Expired {date}",
    "view.encrypted": "Encrypted",

    "expiry.default": "default",
    "expiry.never": "Never",
    "expiry.day": "1 day",
    "expiry.week": "1 week",
    "expiry.month": "30 days",

    "share.title": "Share options",
    "share.expiry": "Expires",
    "share.encrypt": "Encrypt",
    "share.encrypt.desc": "Encrypts the note in Obsidian before it is sent. The server never sees the password.",
    "share.password": "Password",
    "share.password.empty": "The password must not be empty.",
    "share.password.invalid": "The password contains an invalid character (a lone surrogate). Remove or replace it.",
    "share.password.short": "A short password is easy to crack if the link without it leaks",
    "share.passwordInLink": "Include password in link",
    "share.passwordInLink.desc": "Anyone with the link can read the note. Off: share the password separately.",
    "share.submit": "Share",
    "share.cancel": "Cancel"
};

export type TranslationKey = keyof typeof en;

const fi: Record<TranslationKey, string> = {
    "command.openView": "Avaa Lappu-näkymä",
    "command.share": "Jaa nykyinen tiedosto lappuna",
    "command.copy": "Kopioi nykyisen tiedoston Lappu-linkki",
    "command.update": "Päivitä nykyisen tiedoston lappu",
    "command.delete": "Poista nykyisen tiedoston lappu",
    "command.import": "Tuo jaot Just Share Pleasesta",

    "action.share": "Jaa lappuna",
    "action.copy": "Kopioi Lappu-linkki",
    "action.update": "Päivitä lappu",
    "action.delete": "Poista lappu",
    "action.open": "Avaa Obsidianissa",
    "action.copyPassword": "Kopioi salasana",

    "notice.shared": "{name} jaettiin ja linkki kopioitiin leikepöydälle",
    "notice.updated": "Muistiinpanon {name} lappu päivitettiin",
    "notice.deleted": "Muistiinpanon {name} lappu poistettiin",
    "notice.localDeleted": "Muistiinpanon {name} paikalliset jakotiedot poistettiin",
    "notice.linkCopied": "Muistiinpanon {name} linkki kopioitiin leikepöydälle",
    "notice.notConfigured": "Aseta ensin palvelimen URL ja token Lappu-pluginin asetuksissa.",
    "notice.expiryUnsupported": "Palvelin ei asettanut vanhenemisaikaa, joten {name} pysyy jaettuna, kunnes poistat sen.",
    "notice.encryptedNoPassword": "{name} on merkitty salatuksi, mutta salasana puuttuu, joten sitä ei päivitetty (mitään ei lähetetty).",
    "notice.linkCopyFailed": "{name} jaettiin, mutta linkin kopiointi epäonnistui. Käytä linkin kopiointitoimintoa.",
    "notice.encryptionUnsupported": "Palvelin ei vahvistanut salausta, joten {name} voi näkyä lukukelvottomana tekstinä. Päivitä palvelin.",
    "notice.passwordCopied": "Muistiinpanon {name} salasana kopioitiin leikepöydälle",
    "notice.embedsSkipped": "{count} upotusta ei jaettu: {list}",
    "notice.attachmentsUnsupported": "Palvelin ei tue liitteitä, joten kuvat eivät näy muistiinpanossa {name}. Päivitä palvelin.",

    "button.openSettings": "Avaa asetukset",
    "button.forceDeleteLocal": "Poista paikalliset tiedot väkisin",
    "button.removeLocal": "Poista paikalliset tiedot",

    "error.share": "Virhe jaettaessa muistiinpanoa {name}:",
    "error.update": "Virhe päivitettäessä muistiinpanoa {name}:",
    "error.delete": "Virhe poistettaessa muistiinpanoa {name}:",
    "error.invalidToken": "Token puuttuu tai on väärä – tarkista asetukset.",
    "error.wrongPassword": "Palvelin hylkäsi jaon salasanan.",
    "error.notFound": "Jakoa ei enää ole palvelimella. Voit poistaa sen paikalliset tiedot.",
    "error.tooLarge": "Muistiinpano on liian suuri palvelimelle.",
    "error.attachmentTooLarge": "Kuva {name} on liian suuri palvelimelle (oletusrajat: 10 MiB kuvaa kohti, 50 MiB muistiinpanoa kohti).",
    "error.attachmentFailed": "Kuvan {name} lähettäminen epäonnistui:",
    "error.rateLimited": "Liian monta epäonnistunutta yritystä. Yritä myöhemmin uudelleen.",
    "error.other": "Palvelin vastasi tilakoodilla {status}.",

    "import.offer": "Aiemman Just Share Please -pluginin jakoja löytyi.",
    "import.button": "Tuo jaot Just Share Pleasesta",
    "import.done": "Asetukset ja {count} uutta jakoa tuotiin Just Share Pleasesta.",
    "import.notFound": "Tästä holvista ei löytynyt Just Share Pleasen tietoja.",
    "import.failed": "Tuonti Just Share Pleasesta epäonnistui: {error}",

    "settings.url.name": "Palvelimen URL",
    "settings.url.desc": "Oman Lappu-palvelimesi osoite, esimerkiksi https://lappu.example.com.",
    "settings.token.name": "Token",
    "settings.token.desc": "Palvelimelle määritetty lähetystoken. Se lähetetään jaettaessa, päivitettäessä ja poistettaessa.",
    "settings.includeProperties.name": "Sisällytä tiedoston ominaisuudet",
    "settings.includeProperties.desc": "Näytetäänkö jaetun muistiinpanon ominaisuudet (frontmatter) jaossa.",
    "settings.includeNoteName.name": "Sisällytä muistiinpanon nimi",
    "settings.includeNoteName.desc": "Lisätäänkö jaetun muistiinpanon nimi jakoon otsikoksi.",
    "settings.unshareDeleted.name": "Peru poistettujen tiedostojen jako",
    "settings.unshareDeleted.desc": "Poistetaanko tiedoston jako automaattisesti, kun tiedosto poistetaan. Toimii vain, kun tiedosto poistetaan Obsidianissa.",
    "settings.autoUpdate.name": "Päivitä jaot automaattisesti",
    "settings.autoUpdate.desc": "Päivitetäänkö tiedoston jako automaattisesti, kun tiedostoa muutetaan Obsidianissa.",
    "settings.defaultExpiry.name": "Oletusvanhenemisaika",
    "settings.defaultExpiry.desc": "Kuinka kauan uusi jako on voimassa oletuksena. Voit valita toisen ajan jakaessasi.",
    "settings.defaultEncrypt.name": "Salaa uudet jaot oletuksena",
    "settings.defaultEncrypt.desc": "Onko salaus oletuksena päällä jakaessa. Huomaa: salatun jaon salasana tallennetaan tämän pluginin data.json-tiedostoon holvissasi.",
    "settings.language.name": "Kieli",
    "settings.language.desc": "Tämän pluginin kieli. Komentojen nimet vaihtuvat, kun Obsidian käynnistetään uudelleen.",
    "settings.language.auto": "Automaattinen",
    "settings.language.fi": "Suomi",
    "settings.language.en": "English",
    "settings.import.name": "Tuo Just Share Pleasesta",
    "settings.import.desc": "Kopioi Just Share Please (Self-hosted)- ja alkuperäisen Just Share Please -pluginin jaot ja asetukset. Nykyiset jaot säilyvät. Palvelimen URL ja self-hosted-version token otetaan vain, jos niitä ei ole vielä asetettu.",

    "view.title": "Lappu",
    "view.empty": "Et ole vielä jakanut mitään.",
    "view.fileMissing": "Tiedostoa ei ole enää olemassa",
    "view.expires": "Vanhenee {date}",
    "view.expired": "Vanhentunut {date}",
    "view.encrypted": "Salattu",

    "expiry.default": "oletus",
    "expiry.never": "Ei koskaan",
    "expiry.day": "1 päivä",
    "expiry.week": "1 viikko",
    "expiry.month": "30 päivää",

    "share.title": "Jaon asetukset",
    "share.expiry": "Vanhenee",
    "share.encrypt": "Salaa",
    "share.encrypt.desc": "Salaa muistiinpanon Obsidianissa ennen lähetystä. Palvelin ei koskaan näe salasanaa.",
    "share.password": "Salasana",
    "share.password.empty": "Salasana ei saa olla tyhjä.",
    "share.password.invalid": "Salasanassa on virheellinen merkki (yksinäinen surrogaatti). Poista tai korvaa se.",
    "share.password.short": "Lyhyt salasana on helppo murtaa, jos linkki ilman salasanaa leviää",
    "share.passwordInLink": "Liitä salasana linkkiin",
    "share.passwordInLink.desc": "Kuka tahansa linkin saanut voi lukea muistiinpanon. Pois päältä: jaa salasana erikseen.",
    "share.submit": "Jaa",
    "share.cancel": "Peruuta"
};

export const translations: Record<Language, Record<TranslationKey, string>> = {en, fi};

/**
 * Picks the UI language. An explicit setting wins; otherwise Obsidian's
 * language decides (Finnish for any "fi" locale, English for everything else).
 */
export function resolveLanguage(setting: LanguageSetting | undefined, obsidianLanguage: string | null | undefined): Language {
    if (setting === "fi" || setting === "en")
        return setting;
    let base = (obsidianLanguage ?? "").trim().toLowerCase().split(/[-_]/)[0];
    return base === "fi" ? "fi" : "en";
}

/** Looks up a string and replaces `{param}` placeholders. */
export function translate(language: Language, key: TranslationKey, params: Record<string, string | number> = {}): string {
    let text = translations[language][key] ?? translations.en[key] ?? key;
    return text.replace(/\{(\w+)}/g, (match, name: string) => name in params ? String(params[name]) : match);
}
