// Pure helper functions for the viewer. Works both in the browser
// (exposed as globalThis.LappuViewer) and in Node (module.exports) for tests.
(function (root) {
    "use strict";

    const ID_PATTERN = /^[A-Za-z0-9]{8,64}$/;

    // Hash forms: #<id>, #<id>-<anchor>, #<id>~<key>, #<id>~<key>-<anchor>.
    // The id ends at the first "~" or "-". After a "~" the key runs to the next
    // "-" (the plugin percent-encodes "-" and "~" inside it) and is URI-decoded.
    // key is null when absent, empty or not valid percent-encoding; anchor is
    // null when there is no "-" after the id/key.
    function parseHash(hash) {
        if (!hash)
            return { id: undefined, key: null, anchor: null };
        const body = hash.substring(1);
        const end = body.search(/[~-]/);
        if (end < 0)
            return { id: body, key: null, anchor: null };
        const id = body.substring(0, end);
        let rest = body.substring(end);
        let key = null;
        if (rest[0] === "~") {
            const dash = rest.indexOf("-");
            const rawKey = rest.substring(1, dash < 0 ? rest.length : dash);
            rest = dash < 0 ? "" : rest.substring(dash);
            try {
                key = rawKey ? decodeURIComponent(rawKey) : null;
            } catch (e) {
                key = null;
            }
        }
        return { id, key, anchor: rest ? rest.substring(1) : null };
    }

    // Hashes consist of the file id, a dash and then the permalink anchor name
    // (an optional "~key" may follow the id, see parseHash).
    function getIdFromHash(hash) {
        return parseHash(hash).id;
    }

    // Where the viewer is. Legacy pages ("/") carry everything in the hash (see
    // parseHash). Share pages ("/s/<id>") take the id from the path; their hash
    // may hold "~<key>" and/or the anchor, either as "-<anchor>" or as
    // "<id>-<anchor>" (the form of the heading permalinks and footnote links,
    // whose element ids are "<id>-<slug>").
    // Returns { id, key, anchor, sharePath }: sharePath is true on a share page.
    // When the hash of a share page names another share (an in-note "#<other id>"
    // link), the result is that hash's legacy reading with sharePath false.
    function parseLocation(pathname, hash) {
        const parsed = parseHash(hash);
        const match = /^\/s\/([^/]*)$/.exec(pathname || "");
        if (!match)
            return { ...parsed, sharePath: false };
        const id = match[1];
        if (parsed.id === undefined || parsed.id === "" || parsed.id === id)
            return { id, key: parsed.key, anchor: parsed.anchor, sharePath: true };
        return { ...parsed, sharePath: false };
    }

    // "home" when no id is given, otherwise "valid" or "invalid".
    function classifyId(id) {
        if (id === undefined || id === null || id === "")
            return "home";
        return ID_PATTERN.test(id) ? "valid" : "invalid";
    }

    function escapeHtml(value) {
        if (value === undefined || value === null)
            return "";
        return String(value)
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#39;");
    }

    // Splits "a {x} b" into text and parameter parts, so the caller can build
    // DOM nodes (text nodes, <code>, <a>) without ever parsing strings as HTML.
    function formatParts(template, params) {
        const parts = [];
        const re = /\{(\w+)\}/g;
        let text = "";
        let last = 0;
        let match;
        while ((match = re.exec(template)) !== null) {
            const name = match[1];
            if (!params || !Object.prototype.hasOwnProperty.call(params, name))
                continue;
            text += template.substring(last, match.index);
            if (text)
                parts.push({ type: "text", value: text });
            text = "";
            parts.push({ type: "param", name, value: params[name] });
            last = re.lastIndex;
        }
        text += template.substring(last);
        if (text)
            parts.push({ type: "text", value: text });
        return parts;
    }

    // Makes a note title usable as a file name: removes path separators,
    // characters that are invalid in file names and control characters.
    function safeFileName(title) {
        const name = String(title ?? "")
            .replace(/[\u0000-\u001f\u007f/\\:*?"<>|]/g, "")
            .trim();
        return name.replace(/^\.+$/, "") || "note";
    }

    // Every value is encoded with encodeURIComponent, so a title such as
    // "x&overwrite=true&file=..." cannot add parameters to the URL.
    function buildObsidianNewUrl(title, content) {
        return `obsidian://new?name=${encodeURIComponent(safeFileName(title))}&content=${encodeURIComponent(content)}`;
    }

    // Appends a "- [title](link)" line to today's daily note (Daily notes core plugin).
    function buildObsidianDailyUrl(title, link) {
        const label = String(title ?? "").replace(/[\r\n]+/g, " ").replace(/([\\[\]])/g, "\\$1").trim() || "note";
        const content = `- [${label}](<${String(link).replace(/[<>\s]/g, encodeURIComponent)}>)`;
        return `obsidian://daily?content=${encodeURIComponent(content)}&append=true`;
    }

    // The link to a share without the heading anchor; the decryption key stays
    // only when keepKey is set. A share page keeps its "/s/<id>" form, a legacy
    // page its "/#<id>" form.
    function shareLink(href, keepKey) {
        const url = new URL(href);
        const { id, key, sharePath } = parseLocation(url.pathname, url.hash);
        // the key is encoded like the plugin's encodeLinkKey (src/encryption.ts)
        const keyPart = keepKey && key ? `~${encodeURIComponent(key).replace(/-/g, "%2D").replace(/~/g, "%7E")}` : "";
        url.hash = sharePath ? keyPart : id ? id + keyPart : "";
        return url.href;
    }

    // Page metadata for clippers (e.g. Obsidian Web Clipper): meta tags and a
    // schema.org Article. Encrypted notes never put their content here, only
    // dates and the keyless link. `created` and `expires` are unix seconds.
    function noteMetadata({ title, description, url, created, expires, encrypted, siteName }) {
        const iso = seconds => typeof seconds === "number" && Number.isFinite(seconds)
            && !Number.isNaN(new Date(seconds * 1000).getTime()) ? new Date(seconds * 1000).toISOString() : null;
        const published = iso(created);
        const expiresAt = iso(expires);
        const headline = encrypted ? null : (title || null);
        const summary = encrypted ? null : (description || null);
        const tags = [
            { attr: "property", key: "og:site_name", value: siteName },
            { attr: "property", key: "og:type", value: "article" },
            { attr: "property", key: "og:url", value: url },
            { attr: "property", key: "og:title", value: headline },
            { attr: "name", key: "description", value: summary },
            { attr: "property", key: "og:description", value: summary },
            { attr: "property", key: "article:published_time", value: published },
            { attr: "property", key: "article:expiration_time", value: expiresAt }
        ].filter(tag => tag.value);
        const schema = { "@context": "https://schema.org", "@type": "Article", url, publisher: { "@type": "Organization", name: siteName } };
        if (headline)
            schema.headline = headline;
        if (summary)
            schema.description = summary;
        if (published)
            schema.datePublished = published;
        if (expiresAt)
            schema.expires = expiresAt;
        return { tags, schema };
    }

    // The page title for a note: the generic one for an encrypted note, so the
    // decrypted heading never reaches the title (history, bookmarks, tab lists).
    function pageTitle(heading, encrypted, genericTitle) {
        return encrypted || !heading ? genericTitle : heading;
    }

    function buildDownloadHref(content) {
        return `data:text/plain;charset=utf-8,${encodeURIComponent(content)}`;
    }

    function dateFromSeconds(seconds) {
        if (typeof seconds !== "number" || !Number.isFinite(seconds))
            return null;
        const date = new Date(seconds * 1000);
        return Number.isNaN(date.getTime()) ? null : date;
    }

    // Formats unix seconds as a date and time (no seconds) in the given language,
    // or null when the value is not a valid time.
    function formatTimestamp(seconds, lang) {
        const date = dateFromSeconds(seconds);
        return date && date.toLocaleString(lang, {
            day: "numeric",
            month: "numeric",
            year: "numeric",
            hour: "2-digit",
            minute: "2-digit"
        });
    }

    // Formats unix seconds as a date without the time, or null like formatTimestamp.
    function formatDate(seconds, lang) {
        const date = dateFromSeconds(seconds);
        return date && date.toLocaleDateString(lang, { day: "numeric", month: "numeric", year: "numeric" });
    }

    // The source code link set by the server (data-source-url), or null unless it is an absolute http(s) URL.
    function sourceLink(value) {
        if (typeof value !== "string" || value === "")
            return null;
        try {
            const url = new URL(value);
            return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
        } catch (e) {
            return null;
        }
    }

    // ---- SimpleTextCrypt 5.x decryption (see simplecrypter/src/crypter.ts) ----
    // Text = Base64(IV, 12 B) + Base64(ciphertext || 16 B GCM tag), whitespace
    // insensitive. Key = Argon2id(password, salt = IV, t=5, m=64 MiB, p=2, 32 B).

    const IV_LENGTH = 12;
    const TAG_LENGTH = 16;

    // code: "invalid-format" | "decrypt-failed" | "empty-password" | "unavailable"
    class CrypterError extends Error {
        constructor(code, message) {
            super(message);
            this.name = "CrypterError";
            this.code = code;
        }
    }

    function base64ToBytes(base64) {
        let binary;
        try {
            binary = atob(base64);
        } catch (e) {
            throw new CrypterError("invalid-format", "Message is not valid Base64");
        }
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++)
            bytes[i] = binary.charCodeAt(i);
        return bytes;
    }

    // Text form -> IV || ciphertext || tag.
    function decodeMessage(message) {
        const compact = String(message).replace(/\s+/g, "");
        if (compact.length <= 16)
            throw new CrypterError("invalid-format", "Message is too short");
        const iv = base64ToBytes(compact.slice(0, 16));
        const ciphertext = base64ToBytes(compact.slice(16));
        if (iv.length !== IV_LENGTH || ciphertext.length < TAG_LENGTH)
            throw new CrypterError("invalid-format", "Message is too short");
        const packet = new Uint8Array(iv.length + ciphertext.length);
        packet.set(iv);
        packet.set(ciphertext, iv.length);
        return packet;
    }

    // Web Crypto's subtle only exists in secure contexts (https, localhost).
    function getSubtle() {
        return root.crypto && root.crypto.subtle ? root.crypto.subtle : null;
    }

    // hash-wasm's UMD build exposes the global "hashwasm".
    function getArgon2id() {
        return root.hashwasm && root.hashwasm.argon2id ? root.hashwasm.argon2id : null;
    }

    function cryptoAvailable() {
        return getSubtle() !== null && getArgon2id() !== null;
    }

    // deps.argon2id / deps.subtle can be injected (tests); they default to the globals.
    async function deriveKey(password, iv, deps) {
        const argon2id = (deps && deps.argon2id) || getArgon2id();
        const subtle = (deps && deps.subtle) || getSubtle();
        if (!argon2id || !subtle)
            throw new CrypterError("unavailable", "Decryption is not available in this browser context");
        if (!password)
            throw new CrypterError("empty-password", "Password must not be empty");
        const raw = await argon2id({
            password, salt: iv, iterations: 5, memorySize: 65536,
            parallelism: 2, hashLength: 32, outputType: "binary"
        });
        return subtle.importKey("raw", raw, "AES-GCM", false, ["decrypt"]);
    }

    // Decrypts a binary packet (IV || ciphertext || tag) to bytes. Every packet
    // has its own IV and thus its own Argon2 run.
    async function decryptBytes(password, packet, deps) {
        if (!(packet instanceof Uint8Array) || packet.length < IV_LENGTH + TAG_LENGTH)
            throw new CrypterError("invalid-format", "Packet is too short");
        const iv = packet.subarray(0, IV_LENGTH);
        const key = await deriveKey(password, iv, deps);
        const subtle = (deps && deps.subtle) || getSubtle();
        try {
            return new Uint8Array(await subtle.decrypt(
                { name: "AES-GCM", iv, tagLength: 128 }, key, packet.subarray(IV_LENGTH)));
        } catch (e) {
            throw new CrypterError("decrypt-failed", "Wrong password or corrupted message");
        }
    }

    // Decrypts a SimpleTextCrypt message to a UTF-8 string.
    async function decrypt(password, message, deps) {
        const plain = await decryptBytes(password, decodeMessage(message), deps);
        try {
            return new TextDecoder("utf-8", { fatal: true }).decode(plain);
        } catch (e) {
            throw new CrypterError("decrypt-failed", "Decrypted data is not valid UTF-8");
        }
    }

    // ---- Attachments (images stored next to the note, see the server's share.php?att=) ----

    // <sha256 hex>.<ext>; the same rule as the server's. No SVG.
    const ATTACHMENT_PATTERN = /^[a-f0-9]{64}\.(png|jpe?g|gif|webp|avif)$/;
    const ATTACHMENT_SCHEME = "lappu-att:";
    const ATTACHMENT_MIME = {
        png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg",
        gif: "image/gif", webp: "image/webp", avif: "image/avif"
    };
    // 1x1 transparent GIF shown until an encrypted attachment has been decrypted.
    const ATTACHMENT_PLACEHOLDER = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

    function isAttachmentName(name) {
        return typeof name === "string" && ATTACHMENT_PATTERN.test(name);
    }

    // The MIME type for a valid attachment name, otherwise null.
    function attachmentMime(name) {
        return isAttachmentName(name) ? ATTACHMENT_MIME[name.substring(name.lastIndexOf(".") + 1)] : null;
    }

    // The URL of an attachment; only built from a valid share id and name.
    function attachmentUrl(id, name) {
        if (typeof id !== "string" || !ID_PATTERN.test(id) || !isAttachmentName(name))
            return null;
        return `/share.php?att=${id}/${name}`;
    }

    // Decides the src of a markdown image. ctx = { id, encrypted } of the shared
    // note (absent: not a shared note). Returns null when src is not an
    // attachment reference (leave it alone), otherwise { src, name }:
    // - unencrypted share: src is the server URL, no name
    // - encrypted share: src is a placeholder and name must be fetched and decrypted
    // - invalid name or no share: src is "" (nothing is loaded)
    // The URL is always built here from the validated id and name, never taken from the markdown.
    function rewriteAttachmentSrc(src, ctx) {
        if (typeof src !== "string" || !src.toLowerCase().startsWith(ATTACHMENT_SCHEME))
            return null;
        const name = src.substring(ATTACHMENT_SCHEME.length);
        const url = ctx ? attachmentUrl(ctx.id, name) : null;
        if (!url)
            return { src: "", name: null };
        if (ctx.encrypted)
            return { src: ATTACHMENT_PLACEHOLDER, name };
        return { src: url, name: null };
    }

    // ---- Copying a note ----

    // The note's markdown for the clipboard. In a plain share, `![alt](lappu-att:<name>)`
    // references become absolute attachment URLs (the same ones the viewer loads), so
    // the images still work where the text is pasted. Encrypted shares are left as
    // they are: their images only exist as blob: URLs inside this page.
    // ctx = { id, encrypted } of the shared note, absent for other notes.
    function markdownForCopy(markdown, ctx, baseUrl) {
        const text = String(markdown ?? "");
        if (!ctx || ctx.encrypted)
            return text;
        return text.replace(/(\]\(\s*<?)lappu-att:([^\s)>]*)/gi, (match, prefix, name) => {
            const url = attachmentUrl(ctx.id, name);
            return url ? prefix + new URL(url, baseUrl).href : match;
        });
    }

    // A URL from the rendered note made absolute for the clipboard, or null when it
    // means nothing outside this page (blob: images of encrypted shares, the
    // placeholder of a not yet decrypted image, empty or invalid values).
    function copyableUrl(value, baseUrl) {
        if (typeof value !== "string" || value === "" || value === ATTACHMENT_PLACEHOLDER)
            return null;
        let url;
        try {
            url = new URL(value, baseUrl);
        } catch (e) {
            return null;
        }
        return url.protocol === "blob:" ? null : url.href;
    }

    // ---- Table of contents and QR code ----

    // The table of contents of a note from its headings ([{ level, id, text }] in
    // document order): only h2 and h3 count (h1 is the title), and only notes with
    // at least `min` of them get one. Returns [{ id, text, children }] where an
    // h3 is a child of the h2 before it (or top level when there is none).
    function buildToc(headings, min = 3) {
        const entries = (headings || []).filter(h => (h.level === 2 || h.level === 3) && h.id);
        if (entries.length < min)
            return [];
        const toc = [];
        let section = null; // the last h2 entry
        for (const h of entries) {
            const item = { id: h.id, text: h.text, children: [] };
            if (h.level === 3 && section)
                section.children.push(item);
            else
                toc.push(item);
            if (h.level === 2)
                section = item;
        }
        return toc;
    }

    // An SVG path of the dark modules of a QR code (one 1x1 square per module,
    // shifted by `margin` modules), for a viewBox of size + 2 * margin.
    function qrPath(size, isDark, margin) {
        let d = "";
        for (let row = 0; row < size; row++)
            for (let col = 0; col < size; col++)
                if (isDark(row, col))
                    d += `M${col + margin} ${row + margin}h1v1h-1z`;
        return d;
    }

    const api = {
        ID_PATTERN, parseHash, parseLocation, getIdFromHash, classifyId, escapeHtml, formatParts,
        safeFileName, buildObsidianNewUrl, buildObsidianDailyUrl, shareLink, noteMetadata, buildDownloadHref, pageTitle, formatTimestamp, formatDate, sourceLink,
        CrypterError, decodeMessage, deriveKey, decrypt, decryptBytes, cryptoAvailable,
        ATTACHMENT_PATTERN, ATTACHMENT_PLACEHOLDER, isAttachmentName, attachmentMime, attachmentUrl, rewriteAttachmentSrc,
        markdownForCopy, copyableUrl, buildToc, qrPath
    };
    if (typeof module === "object" && module.exports)
        module.exports = api;
    else
        root.LappuViewer = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
