const { parseLocation, classifyId, decrypt, decryptBytes, cryptoAvailable, rewriteAttachmentSrc, attachmentUrl, attachmentMime, markdownForCopy, copyableUrl, formatParts, safeFileName, buildObsidianNewUrl, buildObsidianDailyUrl, shareLink, noteMetadata, buildDownloadHref, pageTitle, formatDate, formatTimestamp, sourceLink, buildToc, qrPath } = LappuViewer;
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

// Images that reference attachments (lappu-att:<name>) are pointed at the server
// (share.php?att=<id>/<name>) or, in encrypted shares, at a placeholder that
// loadEncryptedImages() replaces. The note's id and encryption come from env.attachments.
let defaultImageRule = md.renderer.rules.image;
md.renderer.rules.image = (tokens, idx, options, env, self) => {
    let token = tokens[idx];
    let rewritten = rewriteAttachmentSrc(token.attrGet("src"), env && env.attachments);
    if (rewritten) {
        token.attrSet("src", rewritten.src);
        if (rewritten.name)
            token.attrSet("data-lappu-att", rewritten.name);
    }
    return defaultImageRule(tokens, idx, options, env, self);
};

let main = $("#main");
let attachmentStatus = $("#attachment-status");
let noteFooter = $("#note-footer");
let noteEnd = $("#note-end");
let copyStatus = $("#copy-status");
let qrDialog = $("#qr-dialog");

// the page title of the home page and of encrypted notes (not read from the page:
// the server puts the note's title in the <title> of a plain share's /s/<id> page)
const genericTitle = "Lappu";

let language = LappuI18n.resolveLanguage(readStoredLanguage(), navigator.language);
// what #main currently shows when it is a UI message rather than a note, so it can be re-rendered on language change
let currentMessage = null;
// the last loaded note footer data, so it can be re-rendered on language change
let currentFooter = null;

// the progress of decrypting an encrypted note's images ({ done, total }), or null
let attachmentProgress = null;
// blob: URLs of decrypted images of the current note
let blobUrls = [];

// the shown shared note for the copy button ({ markdown, ctx }), or null when no shared note is shown
let currentNote = null;
// the key of the transient copy confirmation that is shown, or null
let copyStatusKey = null;
let copyStatusTimer = null;
// the element that had focus before the QR dialog opened (the menu's caret)
let qrReturnFocus = null;

// bumped on every display() so that late responses of an abandoned note are ignored
let displayGeneration = 0;

$("#language-select").on("click", "button", e => setLanguage(e.currentTarget.dataset.lang));
// the same copy button and menu above the note and below it
let copyTools = [$("#note-tools"), $("#note-tools-bottom")].map(setUpCopyTools);
setUpQrDialog();
renderAppName();
applyLanguage();

$(window).on("hashchange", e => {
    if (openOtherShare())
        return;
    let oldUrl = e.originalEvent?.oldURL;
    let oldId;
    try {
        let old = new URL(oldUrl);
        oldId = parseLocation(old.pathname, old.hash).id;
    } catch (err) {
        // no old URL: treat it as a change
    }
    if (oldId !== getId())
        display();
});
if (!openOtherShare())
    display();

// On a share page (/s/<id>), a hash that names another share (an in-note "#<id>"
// link) opens that share in its legacy form. Returns whether it navigates away.
function openOtherShare() {
    if (!window.location.pathname.startsWith("/s/") || currentLocation().sharePath)
        return false;
    window.location.replace("/" + window.location.hash);
    return true;
}

function display() {
    let generation = ++displayGeneration;
    showMessage({ key: "loading" });
    // nothing of the previous note may linger: its text is in currentNote, the byline and the metadata
    window.document.title = genericTitle;
    clearNoteMetadata();
    clearNoteFooter();
    clearAttachments();
    let id = getId();
    let kind = classifyId(id);
    if (kind === "invalid") {
        showMessage({ key: "notFound", params: { id }, homeLink: true });
        return;
    }
    let fail = (r, s, e) => {
        if (generation !== displayGeneration)
            return;
        if (r.status === 404)
            showMessage({ key: "notFound", params: { id }, homeLink: true });
        else
            showMessage({ key: "errorLoading", params: { id, details: `${s} ${e}` }, homeLink: true });
    };
    if (kind !== "valid") {
        $.ajax({ method: "get", url: "/index.md", dataType: "text" })
            .then(t => generation === displayGeneration && renderNote(t, id, kind, null), (r, s, e) => fail(r, s, e));
        return;
    }
    // The meta tells whether the note is encrypted; a failing meta request falls back to a plain note.
    let metaRequest = $.ajax({ method: "get", url: `/share.php?meta&id=${encodeURIComponent(id)}`, dataType: "json" })
        .then(m => m, () => null);
    $.ajax({ method: "get", url: `/share.php?id=${encodeURIComponent(id)}`, dataType: "text" })
        .then(t => metaRequest.then(meta => {
            if (generation !== displayGeneration)
                return;
            if (meta && meta.encrypted === true)
                openEncrypted(t, id, meta, generation);
            else
                renderNote(t, id, kind, meta);
        }), (r, s, e) => fail(r, s, e));
}

// Asks for the password (or tries the one in the link) and renders the note when it decrypts.
// The decrypted text only lives in this function's closure, the rendered note and currentNote.
async function openEncrypted(ciphertext, id, meta, generation) {
    if (!cryptoAvailable()) {
        showMessage({ key: "cryptoUnavailable", homeLink: true });
        return;
    }
    let attempt = async password => {
        showMessage({ key: "decrypting" });
        // let the browser paint the busy message before Argon2 blocks the main thread
        await new Promise(resolve => setTimeout(resolve, 50));
        if (generation !== displayGeneration)
            return;
        try {
            let text = await decrypt(password, ciphertext);
            if (generation === displayGeneration)
                renderNote(text, id, "valid", meta, password);
        } catch (e) {
            if (generation !== displayGeneration)
                return;
            if (e && e.code === "decrypt-failed" || e && e.code === "empty-password")
                showPasswordForm(attempt, "wrongPassword");
            else if (e && e.code === "invalid-format")
                showMessage({ key: "invalidEncrypted", homeLink: true });
            else
                showMessage({ key: "cryptoUnavailable", homeLink: true });
        }
    };
    let key = currentLocation().key;
    if (key)
        await attempt(key);
    else
        showPasswordForm(attempt, null);
}

function showPasswordForm(attempt, errorKey) {
    showMessage({ key: "encryptedNote", form: { attempt, errorKey } });
}

// password is only given for encrypted notes; it is used to decrypt their images.
function renderNote(t, id, kind, meta, password) {
    currentMessage = null;
    let encrypted = meta && meta.encrypted === true;
    let env = kind === "valid" ? { attachments: { id, encrypted } } : {};
    // assigned via innerHTML (not jQuery .html()) so that nothing that slipped through sanitizing runs as a script
    main[0].innerHTML = DOMPurify.sanitize(md.render(t, env));
    renderToc(id);
    // only shared notes get the copy button, not the home page
    currentNote = kind === "valid" ? { markdown: t, ctx: env.attachments } : null;
    setNoteToolsVisible(currentNote !== null);
    if (kind === "valid" && encrypted && password)
        loadEncryptedImages(id, password, displayGeneration);

    $(() => {
        // change title
        let firstHeading = $("h1, h2, h3, h4, h5, h6").first();
        let heading = "";
        if (firstHeading) {
            heading = firstHeading.text().trim();
            if (heading.endsWith("#"))
                heading = heading.substring(0, heading.length - 1).trimEnd();
        }
        window.document.title = pageTitle(heading, encrypted, genericTitle);

        // scroll to anchor (looked up by id rather than as a jQuery selector, so the hash is never parsed as a selector)
        let element = findHashTarget();
        if (element)
            $(window).scrollTop($(element).offset().top);

        if (currentNote)
            currentNote.title = heading;

        if (kind === "valid")
            setNoteMetadata(heading, encrypted, meta);

        // the dates under the title and the quote below the note (only for shared notes)
        if (kind === "valid")
            loadNoteFooter(id, meta);
    });
}

function clearAttachments() {
    for (let url of blobUrls)
        URL.revokeObjectURL(url);
    blobUrls = [];
    attachmentProgress = null;
    renderAttachmentProgress();
}

function renderAttachmentProgress() {
    attachmentStatus.text(attachmentProgress ? t("decryptingImages")
        .replace("{done}", attachmentProgress.done).replace("{total}", attachmentProgress.total) : "");
}

// Fetches, decrypts and shows the images of an encrypted note one at a time:
// every attachment has its own Argon2 run, which blocks the main thread.
async function loadEncryptedImages(id, password, generation) {
    let images = new Map(); // name -> <img> elements
    for (let img of main[0].querySelectorAll("img[data-lappu-att]")) {
        let name = img.getAttribute("data-lappu-att");
        if (!attachmentUrl(id, name)) {
            markImageBroken(img);
            continue;
        }
        if (!images.has(name))
            images.set(name, []);
        images.get(name).push(img);
    }
    if (!images.size)
        return;
    attachmentProgress = { done: 0, total: images.size };
    renderAttachmentProgress();
    for (let [name, elements] of images) {
        // let the browser paint the progress before Argon2 blocks the main thread
        await new Promise(resolve => setTimeout(resolve, 50));
        if (generation !== displayGeneration)
            return;
        let blobUrl = null;
        try {
            let response = await fetch(attachmentUrl(id, name));
            if (!response.ok)
                throw new Error(`HTTP ${response.status}`);
            let packet = new Uint8Array(await response.arrayBuffer());
            let bytes = await decryptBytes(password, packet);
            blobUrl = URL.createObjectURL(new Blob([bytes], { type: attachmentMime(name) }));
        } catch (e) {
            // shown as a broken image below
        }
        if (generation !== displayGeneration) {
            if (blobUrl)
                URL.revokeObjectURL(blobUrl);
            return;
        }
        if (blobUrl)
            blobUrls.push(blobUrl);
        for (let img of elements) {
            if (blobUrl)
                img.src = blobUrl;
            else
                markImageBroken(img);
        }
        attachmentProgress = { done: attachmentProgress.done + 1, total: attachmentProgress.total };
        renderAttachmentProgress();
    }
    attachmentProgress = null;
    renderAttachmentProgress();
}

function markImageBroken(img) {
    img.removeAttribute("src");
    img.classList.add("attachment-broken");
    img.title = t("imageFailed");
}

// A collapsible table of contents after the title (and the dates line, which
// renderNoteFooter() puts right after the title). Its links are the same
// "#<id>-<slug>" hashes as the heading permalinks, so they scroll without
// reloading the note on both "/#<id>" and "/s/<id>" pages. Only headings with
// such an id (from markdown-it-anchor) are listed.
function renderToc(id) {
    let prefix = `${id ?? ""}-`;
    let headings = Array.from(main[0].querySelectorAll("h2, h3"))
        .filter(h => h.id.startsWith(prefix))
        .map(h => {
            let copy = h.cloneNode(true);
            for (let a of copy.querySelectorAll(".header-anchor"))
                a.remove();
            return { level: Number(h.tagName[1]), id: h.id, text: copy.textContent.trim() };
        });
    let toc = buildToc(headings);
    if (!toc.length)
        return;
    let list = items => $("<ul>").append(items.map(item => $("<li>").append(
        $("<a>").attr("href", `#${encodeURIComponent(item.id)}`).text(item.text),
        item.children.length ? list(item.children) : null)));
    let details = $("<details>").addClass("toc").append($("<summary>").addClass("toc-title").text(t("contents")), list(toc));
    // open on wide screens, collapsed on narrow ones
    details[0].open = window.matchMedia("(min-width: 48em)").matches;
    let first = main.children().first();
    if (first.is("h1, h2, h3, h4, h5, h6"))
        first.after(details);
    else
        main.prepend(details);
}

// Shows the dates right away and the quote once it has loaded.
function loadNoteFooter(id, meta) {
    currentFooter = { meta, quote: null };
    renderNoteFooter();
    $.ajax({ method: "get", url: "/share.php?quote", dataType: "json" }).catch(() => null).then(quote => {
        // ignore the result if another note was opened in the meantime
        if (getId() !== id || !currentFooter)
            return;
        currentFooter = { meta, quote };
        renderNoteFooter();
    });
}

function clearNoteFooter() {
    currentFooter = null;
    renderNoteFooter();
}

function renderNoteFooter() {
    noteFooter.empty();
    main.find(".note-byline").remove();
    if (!currentFooter)
        return;
    let { meta, quote } = currentFooter;

    // "Shared 2.11.2026 · expires 9.11.2026" under the first heading; the full time is in the tooltip
    let dates = [];
    for (let [key, seconds] of [["shared", meta && meta.created], ["expires", meta && meta.expires]]) {
        let date = formatDate(seconds, language);
        if (!date)
            continue;
        let text = t(key).replace("{date}", date);
        // the line starts with a capital even when it only has the (lowercase) expiry
        if (!dates.length)
            text = text.charAt(0).toUpperCase() + text.substring(1);
        dates.push($("<span>").attr("title", formatTimestamp(seconds, language)).text(text));
    }
    if (dates.length) {
        let byline = $("<div>").addClass("note-byline");
        dates.forEach((date, i) => byline.append(i ? " · " : "", date));
        let first = main.children().first();
        if (first.is("h1, h2, h3, h4, h5, h6"))
            first.after(byline);
        else
            main.prepend(byline);
    }

    // quote (always inserted as text, never as HTML)
    if (quote && quote.quote) {
        noteFooter.append($("<p>").addClass("note-quote").text(`"${quote.quote}"`));
        if (quote.source)
            noteFooter.append($("<p>").addClass("note-quote-source").text(String(quote.source)));
    }
}

// Shows a UI message in #main. Parameters are rendered as <code> text nodes.
function showMessage(message) {
    currentMessage = message;
    // a message is never a note, so there is nothing to copy
    currentNote = null;
    setNoteToolsVisible(false);
    renderMessage();
}

function renderMessage() {
    if (!currentMessage)
        return;
    let p = $("<p>").append(buildNodes(t(currentMessage.key), currentMessage.params || {}, value => $("<code>").text(value)));
    let box = $("<div>").addClass("center-message").append(p);
    if (currentMessage.form)
        box.append(buildPasswordForm(currentMessage.form));
    if (currentMessage.homeLink)
        box.append($("<p>").append($("<a>").attr("href", "/").text(t("home"))));
    main.empty().append(box);
}

// The password only ever goes from the input to the decrypt function in the browser.
function buildPasswordForm({ attempt, errorKey }) {
    let input = $("<input>").attr({ type: "password", autocomplete: "off", "aria-label": t("password"), placeholder: t("password") });
    let form = $("<form>").addClass("password-form").append(input, $("<button>").attr("type", "submit").text(t("openNote")));
    form.on("submit", e => {
        e.preventDefault();
        let password = String(input.val());
        input.val("");
        attempt(password);
    });
    let box = $("<div>").append(form);
    if (errorKey)
        box.append($("<p>").addClass("password-error").attr("role", "alert").text(t(errorKey)));
    setTimeout(() => input[0] && input[0].focus(), 0);
    return box;
}

// Turns a translated template into DOM nodes; text is always inserted as text nodes.
function buildNodes(template, params, renderParam) {
    return formatParts(template, params).map(part =>
        part.type === "text" ? document.createTextNode(part.value) : renderParam(String(part.value), part.name));
}

function renderStaticStrings() {
    $(".copy-note-label").text(t("copy"));
    $(".copy-more").attr({ "aria-label": t("copyOptions"), title: t("copyOptions") });
    $(".copy-menu").attr("aria-label", t("copyOptions"));
    $(".copy-as-markdown").text(t("copyMarkdown"));
    $(".copy-as-plain").text(t("copyPlainText"));
    $(".copy-as-html").text(t("copyHtml"));
    $(".add-to-obsidian").text(t("addToObsidian"));
    $(".add-to-daily-note").text(t("addToDailyNote"));
    $(".download-markdown").text(t("downloadMarkdown"));
    $(".show-qr").text(t("showQrCode"));
    $(".toc-title").text(t("contents"));
    qrDialog.find(".qr-title").text(t("qrCode"));
    qrDialog.find(".qr-code").attr("aria-label", t("qrCode"));
    qrDialog.find(".qr-password").text(t("qrHasPassword"));
    qrDialog.find(".qr-close").text(t("close"));
    $("#language-select").attr("aria-label", t("language"));
    $("#language-select button").each((_, b) => b.setAttribute("aria-pressed", String(b.dataset.lang === language)));
}

function applyLanguage() {
    document.documentElement.lang = language;
    renderStaticStrings();
    renderMessage();
    renderNoteFooter();
    renderAttachmentProgress();
    renderCopyStatus();
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
    // element ids are "<note id>-<slug>"; the optional "~key" part of the hash is not part of them
    let { id, anchor } = currentLocation();
    if (id === undefined || id === "" && anchor === null)
        return null;
    let target = anchor === null ? id : `${id}-${anchor}`;
    try {
        target = decodeURIComponent(target);
    } catch (e) {
        // keep the raw value if it is not valid percent-encoding
    }
    return document.getElementById(target);
}

// The id, key and anchor of the current page, from "/#<id>…" or "/s/<id>#…" (see parseLocation).
function currentLocation() {
    return parseLocation(window.location.pathname, window.location.hash);
}

function getId() {
    return currentLocation().id;
}

// ---- Copy button: the main half copies Markdown, the caret opens a menu of formats and actions ----

// Wires one copy button and its menu; returns { close, setVisible }.
function setUpCopyTools(tools) {
    let button = tools.find(".copy-note");
    let more = tools.find(".copy-more");
    let menu = tools.find(".copy-menu");
    let items = () => Array.from(menu[0].querySelectorAll("button"));
    // index -1 focuses the last item
    let openMenu = index => {
        menu[0].hidden = false;
        more.attr("aria-expanded", "true");
        let list = items();
        list[(index + list.length) % list.length].focus();
    };
    let close = returnFocus => {
        if (menu[0].hidden)
            return;
        menu[0].hidden = true;
        more.attr("aria-expanded", "false");
        if (returnFocus)
            more[0].focus();
    };

    button.on("click", () => copyNote("markdown"));
    more.on("click", () => (menu[0].hidden ? openMenu(0) : close(false)));
    more.on("keydown", e => {
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            openMenu(e.key === "ArrowDown" ? 0 : -1);
        }
    });
    menu.on("click", "button", e => {
        close(true);
        let { action, format } = e.currentTarget.dataset;
        if (action)
            runNoteAction(action);
        else
            copyNote(format);
    });
    menu.on("keydown", e => {
        let list = items();
        let index = list.indexOf(document.activeElement);
        let move = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: list.length - 1 }[e.key];
        if (move !== undefined) {
            e.preventDefault();
            list[(move + list.length) % list.length].focus();
        } else if (e.key === "Escape") {
            e.preventDefault();
            close(true);
        } else if (e.key === "Tab") {
            close(false);
        }
    });
    // a click or tap anywhere outside the button and the menu closes the menu
    $(document).on("pointerdown", e => {
        if (!menu[0].hidden && !tools[0].contains(e.target))
            close(false);
    });
    return {
        close,
        setVisible: visible => {
            close(false);
            tools[0].hidden = !visible;
        }
    };
}

function setNoteToolsVisible(visible) {
    copyTools[0].setVisible(visible);
    noteEnd[0].hidden = !visible;
    if (!visible && qrDialog[0].open)
        qrDialog[0].close();
}

// action: "obsidian-new" creates a note with the content, "obsidian-daily" appends
// a link line to today's daily note (the link keeps the key only if the opened link
// had one), "download" saves the Markdown as a file. Attachment references become
// absolute URLs, like in the copied Markdown. "qr" shows the link as a QR code.
function runNoteAction(action) {
    if (!currentNote)
        return;
    if (action === "qr") {
        showQrDialog();
        return;
    }
    let title = currentNote.title || genericTitle;
    let href = window.location.href;
    let markdown = markdownForCopy(currentNote.markdown, currentNote.ctx, href);
    if (action === "download") {
        let link = document.createElement("a");
        link.download = `${safeFileName(title)}.md`;
        link.href = buildDownloadHref(markdown);
        link.click();
    } else {
        window.location.href = action === "obsidian-daily"
            ? buildObsidianDailyUrl(title, shareLink(href, true))
            : buildObsidianNewUrl(title, markdown);
    }
}

// ---- QR code of the share link (a modal <dialog>: Esc and the close button close it) ----

function setUpQrDialog() {
    qrDialog.find(".qr-close").on("click", () => qrDialog[0].close());
    qrDialog.on("close", () => {
        if (qrReturnFocus && document.contains(qrReturnFocus))
            qrReturnFocus.focus();
        qrReturnFocus = null;
    });
}

// The link keeps the key of an encrypted note: it is the link the reader shares anyway.
function showQrDialog() {
    if (typeof qrcode !== "function" || typeof qrDialog[0].showModal !== "function")
        return;
    let link = shareLink(window.location.href, true);
    let qr = qrcode(0, "M");
    qr.addData(link);
    qr.make();
    let size = qr.getModuleCount();
    let margin = 4;
    let ns = "http://www.w3.org/2000/svg";
    let svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", `0 0 ${size + 2 * margin} ${size + 2 * margin}`);
    svg.setAttribute("shape-rendering", "crispEdges");
    svg.setAttribute("aria-hidden", "true");
    // black on white in both themes, so that every scanner reads it
    let background = document.createElementNS(ns, "rect");
    background.setAttribute("width", "100%");
    background.setAttribute("height", "100%");
    background.setAttribute("fill", "#ffffff");
    let modules = document.createElementNS(ns, "path");
    modules.setAttribute("d", qrPath(size, (row, col) => qr.isDark(row, col), margin));
    modules.setAttribute("fill", "#000000");
    svg.append(background, modules);
    qrDialog.find(".qr-code").empty().append(svg);
    qrDialog.find(".qr-link").text(link);
    qrDialog.find(".qr-password")[0].hidden = !currentLocation().key;
    qrReturnFocus = document.activeElement;
    qrDialog[0].showModal();
    qrDialog.find(".qr-close")[0].focus();
}

// The app name in the bottom line links to the source code only when the server sets LAPPU_SOURCE_URL.
function renderAppName() {
    let url = sourceLink(document.body.dataset.sourceUrl);
    if (url)
        $("#app-name").empty().append($("<a>").attr({ href: url, rel: "noopener noreferrer" }).text("Lappu"));
}

// ---- Page metadata for clippers (Obsidian Web Clipper etc.) ----

function setNoteMetadata(heading, encrypted, meta) {
    let firstParagraph = main[0].querySelector("p");
    let description = firstParagraph ? firstParagraph.textContent.replace(/\s+/g, " ").trim() : "";
    if (description.length > 200)
        description = description.substring(0, 199).trimEnd() + "…";
    let { tags, schema } = noteMetadata({
        title: heading,
        description,
        url: shareLink(window.location.href, false),
        created: meta && meta.created,
        expires: meta && meta.expires,
        encrypted,
        siteName: genericTitle
    });
    let head = $("head");
    for (let tag of tags)
        head.append($("<meta>").attr({ [tag.attr]: tag.key, content: tag.value, "data-lappu-meta": "" }));
    head.append($("<script>").attr({ type: "application/ld+json", "data-lappu-meta": "" }).text(JSON.stringify(schema)));
}

function clearNoteMetadata() {
    $("head [data-lappu-meta]").remove();
}

// format: "markdown" | "plain" | "html"
async function copyNote(format) {
    if (!currentNote)
        return;
    try {
        if (format === "html") {
            if (typeof ClipboardItem !== "function" || !navigator.clipboard || !navigator.clipboard.write) {
                showCopyStatus("copyHtmlUnsupported");
                return;
            }
            let html = noteForCopy("html").innerHTML;
            let plain = plainTextOf(noteForCopy("plain"));
            await navigator.clipboard.write([new ClipboardItem({
                "text/html": new Blob([html], { type: "text/html" }),
                "text/plain": new Blob([plain], { type: "text/plain" })
            })]);
        } else {
            let text = format === "plain"
                ? plainTextOf(noteForCopy("plain"))
                : markdownForCopy(currentNote.markdown, currentNote.ctx, window.location.href);
            await navigator.clipboard.writeText(text);
        }
        showCopyStatus("copied");
    } catch (e) {
        // no clipboard API (e.g. plain http) or the browser refused
        showCopyStatus("copyFailed");
    }
}

// A detached copy of the rendered note without the viewer's own bits, with
// URLs that work outside this page. format: "html" | "plain"
function noteForCopy(format) {
    let root = main[0].cloneNode(true);
    root.removeAttribute("id");
    for (let el of root.querySelectorAll(".header-anchor, .note-byline, .toc, script, style"))
        el.remove();
    // KaTeX markup holds the formula twice (MathML and HTML), so keep one: MathML for
    // HTML, the visible text for plain text. DOMPurify has unwrapped the TeX
    // <annotation>, leaving its source as a stray text node in <math>; drop that.
    for (let el of root.querySelectorAll(".katex")) {
        let math = el.querySelector(".katex-mathml math");
        let visual = el.querySelector(".katex-html");
        if (format === "plain") {
            el.replaceWith(document.createTextNode((visual || el).textContent));
        } else if (math) {
            for (let node of Array.from(math.childNodes))
                if (node.nodeType === Node.TEXT_NODE)
                    node.remove();
            el.replaceWith(math);
        }
    }
    for (let box of root.querySelectorAll('input[type="checkbox"]')) {
        if (format === "plain") {
            box.replaceWith(document.createTextNode(box.hasAttribute("checked") ? "[x] " : "[ ] "));
        } else {
            box.removeAttribute("id");
            box.setAttribute("disabled", "");
        }
    }
    for (let img of root.querySelectorAll("img")) {
        let src = copyableUrl(img.getAttribute("src"), window.location.href);
        if (!src || img.classList.contains("attachment-broken")) {
            // decrypted (blob:) or missing images mean nothing elsewhere: keep their alt text
            let alt = img.getAttribute("alt");
            img.replaceWith(document.createTextNode(alt ? `[${alt}]` : ""));
            continue;
        }
        img.setAttribute("src", src);
        img.removeAttribute("data-lappu-att");
    }
    for (let a of root.querySelectorAll("a[href]")) {
        let href = copyableUrl(a.getAttribute("href"), window.location.href);
        if (href)
            a.setAttribute("href", href);
        else
            a.removeAttribute("href");
    }
    return root;
}

// innerText needs a rendered element to turn the layout into line breaks, so the
// copy is laid out off screen for a moment.
function plainTextOf(root) {
    root.classList.add("copy-scratch");
    document.body.appendChild(root);
    try {
        return root.innerText.replace(/\n{3,}/g, "\n\n").trim();
    } finally {
        root.remove();
    }
}

function showCopyStatus(key) {
    copyStatusKey = key;
    renderCopyStatus();
    clearTimeout(copyStatusTimer);
    copyStatusTimer = setTimeout(() => {
        copyStatusKey = null;
        renderCopyStatus();
    }, 2500);
}

function renderCopyStatus() {
    copyStatus.text(copyStatusKey ? t(copyStatusKey) : "");
}
