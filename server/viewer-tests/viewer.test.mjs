// Unit tests for the viewer's pure logic (server/public/viewer-lib.js and i18n.js).
// Run with: node --test server/viewer-tests/
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";

const require = createRequire(import.meta.url);
const lib = require("../public/viewer-lib.js");
const i18n = require("../public/i18n.js");

test("getIdFromHash returns undefined for an empty hash", () => {
    assert.equal(lib.getIdFromHash(""), undefined);
    assert.equal(lib.getIdFromHash(undefined), undefined);
});

test("getIdFromHash reads up to the first dash", () => {
    assert.equal(lib.getIdFromHash("#abc123XY"), "abc123XY");
    assert.equal(lib.getIdFromHash("#abc123XY-some-heading"), "abc123XY");
    assert.equal(lib.getIdFromHash("#-heading-on-home"), "");
    assert.equal(lib.getIdFromHash("#"), "");
});

test("classifyId distinguishes home, valid and invalid ids", () => {
    assert.equal(lib.classifyId(undefined), "home");
    assert.equal(lib.classifyId(""), "home");
    assert.equal(lib.classifyId("abcd1234"), "valid");
    assert.equal(lib.classifyId("A".repeat(64)), "valid");
    assert.equal(lib.classifyId("abc1234"), "invalid");
    assert.equal(lib.classifyId("A".repeat(65)), "invalid");
    assert.equal(lib.classifyId("<img src=x onerror=alert(1)>"), "invalid");
    assert.equal(lib.classifyId("abcd1234%3Cb%3E"), "invalid");
    assert.equal(lib.classifyId("abcd_1234"), "invalid");
    assert.equal(lib.classifyId("abcd1234\n"), "invalid");
});

test("escapeHtml escapes all HTML metacharacters", () => {
    assert.equal(lib.escapeHtml(`<a href="x" title='y'>&</a>`),
        "&lt;a href=&quot;x&quot; title=&#39;y&#39;&gt;&amp;&lt;/a&gt;");
    assert.equal(lib.escapeHtml(42), "42");
    assert.equal(lib.escapeHtml(null), "");
});

test("formatParts splits a template into text and parameter parts", () => {
    assert.deepEqual(lib.formatParts("Error {id}: {details}!", { id: "x", details: "<b>" }), [
        { type: "text", value: "Error " },
        { type: "param", name: "id", value: "x" },
        { type: "text", value: ": " },
        { type: "param", name: "details", value: "<b>" },
        { type: "text", value: "!" }
    ]);
    assert.deepEqual(lib.formatParts("{a}", { a: "1" }), [{ type: "param", name: "a", value: "1" }]);
    assert.deepEqual(lib.formatParts("plain", {}), [{ type: "text", value: "plain" }]);
    // unknown placeholders are kept as literal text
    assert.deepEqual(lib.formatParts("x {nope}", {}), [{ type: "text", value: "x {nope}" }]);
});

test("resolveLanguage prefers a stored supported choice", () => {
    assert.equal(i18n.resolveLanguage("en", "fi-FI"), "en");
    assert.equal(i18n.resolveLanguage("fi", "en-US"), "fi");
});

test("resolveLanguage falls back to navigator.language", () => {
    assert.equal(i18n.resolveLanguage(null, "fi-FI"), "fi");
    assert.equal(i18n.resolveLanguage(null, "fi"), "fi");
    assert.equal(i18n.resolveLanguage(null, "FI"), "fi");
    assert.equal(i18n.resolveLanguage(undefined, "en-GB"), "en");
    assert.equal(i18n.resolveLanguage("de", "sv-SE"), "en");
    assert.equal(i18n.resolveLanguage("constructor", undefined), "en");
    assert.equal(i18n.resolveLanguage(null, ""), "en");
});

test("every language defines the same keys", () => {
    const en = Object.keys(i18n.translations.en).sort();
    for (const lang of i18n.languages)
        assert.deepEqual(Object.keys(i18n.translations[lang]).sort(), en, lang);
    assert.deepEqual([...i18n.languages].sort(), ["en", "fi"]);
});

test("translate looks up a key and falls back to English, then the key", () => {
    assert.equal(i18n.translate("fi", "home"), "Etusivu");
    assert.equal(i18n.translate("en", "home"), "Home");
    assert.equal(i18n.translate("xx", "home"), "Home");
    assert.equal(i18n.translate("fi", "noSuchKey"), "noSuchKey");
});

test("safeFileName removes path separators and characters invalid in file names", () => {
    assert.equal(lib.safeFileName("Notes/../Daily\\Plan: a*b?\"<>|"), "Notes..DailyPlan ab");
    assert.equal(lib.safeFileName("a\u0000b\nc"), "abc");
    assert.equal(lib.safeFileName("  ...  "), "note");
    assert.equal(lib.safeFileName(""), "note");
    assert.equal(lib.safeFileName(undefined), "note");
    assert.equal(lib.safeFileName("Hyvä muistiinpano #1"), "Hyvä muistiinpano #1");
});

test("buildObsidianNewUrl cannot be used to inject extra parameters", () => {
    const url = lib.buildObsidianNewUrl("Notes&overwrite=true&file=Daily/Plan", "body & more=1#x");
    assert.ok(url.startsWith("obsidian://new?"));
    const params = new URLSearchParams(url.substring("obsidian://new?".length));
    assert.deepEqual([...params.keys()], ["name", "content"]);
    assert.equal(params.get("name"), "Notes&overwrite=true&file=DailyPlan");
    assert.equal(params.get("content"), "body & more=1#x");
    assert.ok(!url.includes("&overwrite"));
});

test("buildObsidianNewUrl encodes spaces and unicode in the name", () => {
    const url = lib.buildObsidianNewUrl("Hyvä nimi", "x");
    assert.equal(url, "obsidian://new?name=Hyv%C3%A4%20nimi&content=x");
});

test("buildDownloadHref produces a plain-text data URL", () => {
    assert.equal(lib.buildDownloadHref("# a&b"), "data:text/plain;charset=utf-8,%23%20a%26b");
});

test("formatExpires formats unix seconds for the given language", () => {
    const expires = 1_800_000_000;
    assert.equal(lib.formatExpires(expires, "fi"), new Date(expires * 1000).toLocaleString("fi"));
    assert.equal(lib.formatExpires(expires, "en"), new Date(expires * 1000).toLocaleString("en"));
});

test("formatExpires returns null for a missing or invalid expiry", () => {
    for (const value of [null, undefined, "1800000000", NaN, Infinity, 1e20])
        assert.equal(lib.formatExpires(value, "en"), null);
});

test("expires translation has a date placeholder in every language", () => {
    for (const lang of i18n.languages)
        assert.match(i18n.translate(lang, "expires"), /\{date\}/);
    assert.equal(i18n.translate("fi", "expires"), "Vanhenee: {date}");
    assert.equal(i18n.translate("en", "expires"), "Expires: {date}");
});

// ---- link format with an optional key ----

test("parseHash handles all four hash forms", () => {
    assert.deepEqual(lib.parseHash("#abcd1234"), { id: "abcd1234", key: null, anchor: null });
    assert.deepEqual(lib.parseHash("#abcd1234-my-head"), { id: "abcd1234", key: null, anchor: "my-head" });
    assert.deepEqual(lib.parseHash("#abcd1234~secret"), { id: "abcd1234", key: "secret", anchor: null });
    assert.deepEqual(lib.parseHash("#abcd1234~secret-my-head"), { id: "abcd1234", key: "secret", anchor: "my-head" });
    assert.deepEqual(lib.parseHash(""), { id: undefined, key: null, anchor: null });
});

test("parseHash decodes the key, including encoded dashes and tildes", () => {
    const pw = "a-b~c d/ä❤%";
    const enc = encodeURIComponent(pw).replace(/-/g, "%2D").replace(/~/g, "%7E");
    assert.equal(lib.parseHash(`#abcd1234~${enc}`).key, pw);
    assert.deepEqual(lib.parseHash(`#abcd1234~${enc}-sec`), { id: "abcd1234", key: pw, anchor: "sec" });
    assert.equal(lib.getIdFromHash(`#abcd1234~${enc}-sec`), "abcd1234");
});

test("parseHash ignores empty or malformed keys", () => {
    assert.equal(lib.parseHash("#abcd1234~").key, null);
    assert.equal(lib.parseHash("#abcd1234~%E0%A4%A").key, null);
    assert.equal(lib.parseHash("#abcd1234~-x").anchor, "x");
});

// ---- decryption against the SimpleTextCrypt reference vectors ----

const { argon2id } = await import("hash-wasm");
// copied from simplecrypter/test/vectors.json
const vectors = JSON.parse(readFileSync(new URL("./vectors.json", import.meta.url)));
const deps = { argon2id };

test("decodeMessage splits IV and ciphertext and ignores whitespace", () => {
    const v = vectors[0];
    const packet = lib.decodeMessage(v.message);
    assert.equal(Buffer.from(packet.subarray(0, 12)).toString("hex"), v.ivHex);
    assert.deepEqual(lib.decodeMessage(v.message.replace(/\n/g, "  \r\n\t")), packet);
});

test("decodeMessage rejects malformed input with invalid-format", () => {
    for (const bad of ["", "AAECAwQFBgcICQoL", "AAECAwQFBgcICQoLAAAA", "!!!!!!!!!!!!!!!!AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"])
        assert.throws(() => lib.decodeMessage(bad), e => e instanceof lib.CrypterError && e.code === "invalid-format");
});

test("decrypt opens every reference vector", async () => {
    for (const v of vectors)
        assert.equal(await lib.decrypt(v.password, v.message, deps), v.plaintext);
});

test("decrypt reports a wrong password as decrypt-failed", async () => {
    await assert.rejects(lib.decrypt("wrong", vectors[0].message, deps),
        e => e instanceof lib.CrypterError && e.code === "decrypt-failed");
});

test("decrypt reports a corrupted message and an empty password", async () => {
    const m = vectors[0].message.replace(/\s+/g, "");
    const tampered = m.slice(0, -6) + (m.slice(-6, -5) === "A" ? "B" : "A") + m.slice(-5);
    await assert.rejects(lib.decrypt(vectors[0].password, tampered, deps), e => e.code === "decrypt-failed");
    await assert.rejects(lib.decrypt("", vectors[0].message, deps), e => e.code === "empty-password");
});

test("decrypt reports unavailable without argon2id", async () => {
    await assert.rejects(lib.decrypt("x", vectors[0].message, { argon2id: null, subtle: {} }),
        e => e.code === "unavailable");
});
