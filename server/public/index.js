const { parseHash, getIdFromHash, classifyId, decrypt, decryptBytes, cryptoAvailable, rewriteAttachmentSrc, attachmentUrl, attachmentMime, markdownForCopy, copyableUrl, formatParts, safeFileName, buildObsidianNewUrl, buildDownloadHref, pageTitle, formatExpires } = LappuViewer;
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
let download = $("#download-markdown");
let open = $("#open-in-obsidian");
let noteTools = $("#note-tools");
let copyButton = $("#copy-note");
let copyMore = $("#copy-more");
let copyMenu = $("#copy-menu");
let copyStatus = $("#copy-status");

// the page title of the home page and of encrypted notes
const genericTitle = window.document.title;

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

// bumped on every display() so that late responses of an abandoned note are ignored
let displayGeneration = 0;

$("#language-select").on("click", "button", e => setLanguage(e.currentTarget.dataset.lang));
setUpCopyButton();
applyLanguage();

$(window).on("hashchange", e => {
    let oldUrl = e.originalEvent?.oldURL;
    let oldHash = oldUrl?.lastIndexOf("#");
    if (oldHash && getId(oldUrl.substring(oldHash)) !== getId())
        display();
});
display();

function display() {
    let generation = ++displayGeneration;
    showMessage({ key: "loading" });
    // nothing of the previous note may linger: its text is in the download link, the Obsidian link and currentNote
    download.removeAttr("href").removeAttr("download");
    open.removeAttr("href");
    window.document.title = genericTitle;
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
        $.ajax({ method: "get", url: "index.md", dataType: "text" })
            .then(t => generation === displayGeneration && renderNote(t, id, kind, null), (r, s, e) => fail(r, s, e));
        return;
    }
    // The meta tells whether the note is encrypted; a failing meta request falls back to a plain note.
    let metaRequest = $.ajax({ method: "get", url: `share.php?meta&id=${encodeURIComponent(id)}`, dataType: "json" })
        .then(m => m, () => null);
    $.ajax({ method: "get", url: `share.php?id=${encodeURIComponent(id)}`, dataType: "text" })
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
// The decrypted text only lives in this function's closure, the rendered note and its copy/download links.
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
    let key = parseHash(window.location.hash).key;
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

        // set download links
        let noteName = heading || window.document.title;
        download.attr("download", `${safeFileName(noteName)}.md`);
        download.attr("href", buildDownloadHref(t));
        open.attr("href", buildObsidianNewUrl(noteName, t));

        // load note footer with publication time (only for shared notes)
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

function loadNoteFooter(id, meta) {
    $.ajax({ method: "get", url: "share.php?quote", dataType: "json" }).catch(() => null).then(quote => {
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
        box.append($("<p>").append($("<a>").attr("href", "#").text(t("home"))));
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
    $("#footer-credit").empty().append(buildNodes(t("createdUsing"), {
        app: "Lappu",
        obsidian: "Obsidian"
    }, (value, name) => $("<a>").attr("href", name === "app" ? "/" : "https://obsidian.md").text(value)));
    download.text(t("downloadMarkdown"));
    $("#copy-note-label").text(t("copy"));
    copyMore.attr({ "aria-label": t("copyOptions"), title: t("copyOptions") });
    copyMenu.attr("aria-label", t("copyOptions"));
    $("#copy-as-markdown").text(t("copyMarkdown"));
    $("#copy-as-plain").text(t("copyPlainText"));
    $("#copy-as-html").text(t("copyHtml"));
    open.text(t("copyToObsidian"));
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
    let { id, anchor } = parseHash(window.location.hash);
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

function getId(hash) {
    return getIdFromHash(hash || window.location.hash);
}

// ---- Copy button: the main half copies Markdown, the caret opens a menu of formats ----

function setUpCopyButton() {
    copyButton.on("click", () => copyNote("markdown"));
    copyMore.on("click", () => (copyMenu[0].hidden ? openCopyMenu(0) : closeCopyMenu(false)));
    copyMore.on("keydown", e => {
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            openCopyMenu(e.key === "ArrowDown" ? 0 : -1);
        }
    });
    copyMenu.on("click", "button", e => {
        closeCopyMenu(true);
        copyNote(e.currentTarget.dataset.format);
    });
    copyMenu.on("keydown", e => {
        let items = copyMenuItems();
        let index = items.indexOf(document.activeElement);
        let move = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: items.length - 1 }[e.key];
        if (move !== undefined) {
            e.preventDefault();
            items[(move + items.length) % items.length].focus();
        } else if (e.key === "Escape") {
            e.preventDefault();
            closeCopyMenu(true);
        } else if (e.key === "Tab") {
            closeCopyMenu(false);
        }
    });
    // a click or tap anywhere outside the button and the menu closes the menu
    $(document).on("pointerdown", e => {
        if (!copyMenu[0].hidden && !noteTools[0].contains(e.target))
            closeCopyMenu(false);
    });
}

function copyMenuItems() {
    return Array.from(copyMenu[0].querySelectorAll("button"));
}

// index -1 focuses the last item
function openCopyMenu(index) {
    copyMenu[0].hidden = false;
    copyMore.attr("aria-expanded", "true");
    let items = copyMenuItems();
    items[(index + items.length) % items.length].focus();
}

function closeCopyMenu(returnFocus) {
    if (copyMenu[0].hidden)
        return;
    copyMenu[0].hidden = true;
    copyMore.attr("aria-expanded", "false");
    if (returnFocus)
        copyMore[0].focus();
}

function setNoteToolsVisible(visible) {
    closeCopyMenu(false);
    noteTools[0].hidden = !visible;
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
    for (let el of root.querySelectorAll(".header-anchor, script, style"))
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
