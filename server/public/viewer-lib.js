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

    // Decrypts a SimpleTextCrypt message to a UTF-8 string.
    async function decrypt(password, message, deps) {
        const packet = decodeMessage(message);
        const iv = packet.subarray(0, IV_LENGTH);
        const key = await deriveKey(password, iv, deps);
        const subtle = (deps && deps.subtle) || getSubtle();
        let plain;
        try {
            plain = new Uint8Array(await subtle.decrypt(
                { name: "AES-GCM", iv, tagLength: 128 }, key, packet.subarray(IV_LENGTH)));
        } catch (e) {
            throw new CrypterError("decrypt-failed", "Wrong password or corrupted message");
        }
        try {
            return new TextDecoder("utf-8", { fatal: true }).decode(plain);
        } catch (e) {
            throw new CrypterError("decrypt-failed", "Decrypted data is not valid UTF-8");
        }
    }

    const api = {
        ID_PATTERN, parseHash, getIdFromHash, classifyId, escapeHtml, formatParts,
        safeFileName, buildObsidianNewUrl, buildDownloadHref, formatExpires,
        CrypterError, decodeMessage, deriveKey, decrypt, cryptoAvailable
    };
    if (typeof module === "object" && module.exports)
        module.exports = api;
    else
        root.LappuViewer = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
