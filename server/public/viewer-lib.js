// Pure helper functions for the viewer. Works both in the browser
// (exposed as globalThis.LappuViewer) and in Node (module.exports) for tests.
(function (root) {
    "use strict";

    const ID_PATTERN = /^[A-Za-z0-9]{8,64}$/;

    // Hashes consist of the file id, a dash and then the permalink anchor name.
    function getIdFromHash(hash) {
        if (!hash)
            return undefined;
        const dash = hash.indexOf("-");
        return hash.substring(1, dash > 0 ? dash : hash.length);
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

    function buildDownloadHref(content) {
        return `data:text/plain;charset=utf-8,${encodeURIComponent(content)}`;
    }

    // The expiry of a share as a localized date and time, or null when the meta
    // has no (valid) expiry. `expires` is unix seconds.
    function formatExpires(expires, lang) {
        if (typeof expires !== "number" || !Number.isFinite(expires))
            return null;
        const date = new Date(expires * 1000);
        if (Number.isNaN(date.getTime()))
            return null;
        return date.toLocaleString(lang);
    }

    const api = {
        ID_PATTERN, getIdFromHash, classifyId, escapeHtml, formatParts,
        safeFileName, buildObsidianNewUrl, buildDownloadHref, formatExpires
    };
    if (typeof module === "object" && module.exports)
        module.exports = api;
    else
        root.LappuViewer = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
