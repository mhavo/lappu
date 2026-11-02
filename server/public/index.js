const { getIdFromHash, classifyId, formatParts, safeFileName, buildObsidianNewUrl, buildDownloadHref, formatExpires } = LappuViewer;
const LANGUAGE_STORAGE_KEY = "lappu-language";

let md = markdownit({
    html: true,
    linkify: true,
    langPrefix: "hljs language-",
    highlight: (c, l) => {
        const language = hljs.getLanguage(l) ? l : "plaintext";
        return hljs.highlight(c, {language}).value;
    }
});
md.use(texmath, {
    engine: katex,
    delimiters: ["dollars", "beg_end"]
});
md.use(markdownItAnchor, {
    permalink: markdownItAnchor.permalink.linkInsideHeader({
        placement: "after",
        ariaHidden: true
    }),
    // prepend current file id to the anchor permalink
    slugify: s => `${getId() ?? ""}-${encodeURIComponent(String(s).trim().toLowerCase().normalize("NFKD").replace(/\s+/g, "-").replace(/[^a-z0-9_-]/g, ""))}`
});
md.use(markdownitFootnote);
md.use(markdownitCheckbox);

let rulesToReplace = [
    // prepend current file id to footnotes
    ["footnote_ref", /href="#(fn\d+)"/, id => `href="#${id}-$1"`],
    ["footnote_open", /id="(fn\d+)"/, id => `id="${id}-$1"`],
    // prepend current file id to footnote refs
    ["footnote_ref", /id="(fnref\d+)"/, id => `id="${id}-$1"`],
    ["footnote_anchor", /href="#(fnref\d+)"/, id => `href="#${id}-$1"`]
];
for (let [rule, pattern, replacement] of rulesToReplace) {
    let prevRule = md.renderer.rules[rule];
    md.renderer.rules[rule] = (tokens, idx, options, env, self) => {
        // the id is only ever empty or alphanumeric here (validated in display())
        return prevRule(tokens, idx, options, env, self).replace(pattern, replacement(getId() ?? ""));
    };
}

let main = $("#main");
let noteFooter = $("#note-footer");
let download = $("#download-markdown");
let open = $("#open-in-obsidian");

let language = LappuI18n.resolveLanguage(readStoredLanguage(), navigator.language);
// what #main currently shows when it is a UI message rather than a note, so it can be re-rendered on language change
let currentMessage = null;
// the last loaded note footer data, so it can be re-rendered on language change
let currentFooter = null;

$("#language-select").on("click", "button", e => setLanguage(e.currentTarget.dataset.lang));
applyLanguage();

$(window).on("hashchange", e => {
    let oldUrl = e.originalEvent?.oldURL;
    let oldHash = oldUrl?.lastIndexOf("#");
    if (oldHash && getId(oldUrl.substring(oldHash)) !== getId())
        display();
});
display();

function display() {
    showMessage({ key: "loading" });
    clearNoteFooter();
    let id = getId();
    let kind = classifyId(id);
    if (kind === "invalid") {
        showMessage({ key: "notFound", params: { id }, homeLink: true });
        return;
    }
    $.ajax({
        method: "get",
        url: kind === "valid" ? `share.php?id=${encodeURIComponent(id)}` : "index.md",
        dataType: "text",
        success: t => {
            currentMessage = null;
            // assigned via innerHTML (not jQuery .html()) so that nothing that slipped through sanitizing runs as a script
            main[0].innerHTML = DOMPurify.sanitize(md.render(t));

            $(() => {
                // change title
                let firstHeading = $("h1, h2, h3, h4, h5, h6").first();
                if (firstHeading) {
                    let heading = firstHeading.text().trim();
                    if (heading.endsWith("#"))
                        heading = heading.substring(0, heading.length - 1).trimEnd();
                    window.document.title = heading;
                }

                // scroll to anchor (looked up by id rather than as a jQuery selector, so the hash is never parsed as a selector)
                let element = findHashTarget();
                if (element)
                    $(window).scrollTop($(element).offset().top);

                // set download links
                download.attr("download", `${safeFileName(window.document.title)}.md`);
                download.attr("href", buildDownloadHref(t));
                open.attr("href", buildObsidianNewUrl(window.document.title, t));

                // load note footer with publication time (only for shared notes)
                if (kind === "valid") {
                    loadNoteFooter(id);
                }
            });
        },
        error: (r, s, e) => {
            if (r.status === 404)
                showMessage({ key: "notFound", params: { id }, homeLink: true });
            else
                showMessage({ key: "errorLoading", params: { id, details: `${s} ${e}` }, homeLink: true });
        }
    });
}

function loadNoteFooter(id) {
    let encodedId = encodeURIComponent(id);
    // Fetch meta and quote in parallel
    Promise.all([
        $.ajax({ method: "get", url: `share.php?meta&id=${encodedId}`, dataType: "json" }).catch(() => null),
        $.ajax({ method: "get", url: "share.php?quote", dataType: "json" }).catch(() => null)
    ]).then(([meta, quote]) => {
        // ignore the result if another note was opened in the meantime
        if (getId() !== id)
            return;
        currentFooter = { meta, quote };
        renderNoteFooter();
    });
}

function clearNoteFooter() {
    currentFooter = null;
    noteFooter.empty();
}

function renderNoteFooter() {
    noteFooter.empty();
    if (!currentFooter)
        return;
    let { meta, quote } = currentFooter;
    let parts = [];

    // Add timestamp if available
    if (meta && meta.created) {
        let date = new Date(meta.created * 1000);
        let formatted = date.toLocaleDateString(undefined, {
            day: "numeric",
            month: "numeric",
            year: "numeric",
            hour: "2-digit",
            minute: "2-digit"
        });
        parts.push($("<p>").addClass("note-timestamp").text(t("shared").replace("{date}", formatted)));
    }

    // Add expiry if the share has one
    let expires = formatExpires(meta && meta.expires, language);
    if (expires)
        parts.push($("<p>").addClass("note-timestamp").text(t("expires").replace("{date}", expires)));

    // Add quote if available (always inserted as text, never as HTML)
    if (quote && quote.quote) {
        parts.push($("<p>").addClass("note-quote").text(`"${quote.quote}"`));
        if (quote.source) {
            parts.push($("<p>").addClass("note-quote-source").append(document.createTextNode("— "), $("<em>").text(String(quote.source))));
        }
    }

    // Only show footer if there's content
    if (parts.length)
        noteFooter.append($("<div>").addClass("note-footer-divider"), ...parts);
}

// Shows a UI message in #main. Parameters are rendered as <code> text nodes.
function showMessage(message) {
    currentMessage = message;
    renderMessage();
}

function renderMessage() {
    if (!currentMessage)
        return;
    let p = $("<p>").append(buildNodes(t(currentMessage.key), currentMessage.params || {}, value => $("<code>").text(value)));
    let box = $("<div>").addClass("center-message").append(p);
    if (currentMessage.homeLink)
        box.append($("<p>").append($("<a>").attr("href", "#").text(t("home"))));
    main.empty().append(box);
}

// Turns a translated template into DOM nodes; text is always inserted as text nodes.
function buildNodes(template, params, renderParam) {
    return formatParts(template, params).map(part =>
        part.type === "text" ? document.createTextNode(part.value) : renderParam(String(part.value), part.name));
}

function renderStaticStrings() {
    $("#footer-credit").empty().append(buildNodes(t("createdUsing"), {
        app: "Lappu",
        obsidian: "Obsidian"
    }, (value, name) => $("<a>").attr("href", name === "app" ? "/" : "https://obsidian.md").text(value)));
    download.text(t("downloadMarkdown"));
    open.text(t("copyToObsidian"));
    $("#language-select").attr("aria-label", t("language"));
    $("#language-select button").each((_, b) => b.setAttribute("aria-pressed", String(b.dataset.lang === language)));
}

function applyLanguage() {
    document.documentElement.lang = language;
    renderStaticStrings();
    renderMessage();
    renderNoteFooter();
}

function setLanguage(lang) {
    if (!LappuI18n.languages.includes(lang) || lang === language)
        return;
    language = lang;
    try {
        localStorage.setItem(LANGUAGE_STORAGE_KEY, lang);
    } catch (e) {
        // storage may be unavailable (private mode, blocked site data); the choice then lasts for this page only
    }
    applyLanguage();
}

function readStoredLanguage() {
    try {
        return localStorage.getItem(LANGUAGE_STORAGE_KEY);
    } catch (e) {
        return null;
    }
}

function t(key) {
    return LappuI18n.translate(language, key);
}

function findHashTarget() {
    let hash = window.location.hash;
    if (!hash || hash.length < 2)
        return null;
    let target = hash.substring(1);
    try {
        target = decodeURIComponent(target);
    } catch (e) {
        // keep the raw value if it is not valid percent-encoding
    }
    return document.getElementById(target);
}

function getId(hash) {
    return getIdFromHash(hash || window.location.hash);
}
