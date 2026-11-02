import {describe, expect, it} from "vitest";
import {buildShareLink, confirmsEncryption, encodeLinkKey, generatePassword, isValidPassword} from "../src/encryption";

describe("generatePassword", () => {
    it("is 22 base62 characters (>= 128 bits)", () => {
        for (let i = 0; i < 50; i++)
            expect(generatePassword()).toMatch(/^[0-9A-Za-z]{22}$/);
        expect(Math.log2(62) * 22).toBeGreaterThanOrEqual(128);
    });

    it("differs between calls and uses the whole alphabet", () => {
        let seen = new Set<string>();
        for (let i = 0; i < 200; i++)
            for (let c of generatePassword())
                seen.add(c);
        expect(seen.size).toBe(62);
        expect(generatePassword()).not.toBe(generatePassword());
    });

    it("honours an explicit length", () => {
        expect(generatePassword(40)).toHaveLength(40);
    });
});

describe("isValidPassword", () => {
    it("rejects empty and blank passwords", () => {
        expect(isValidPassword("")).toBe(false);
        expect(isValidPassword("   ")).toBe(false);
        expect(isValidPassword("a")).toBe(true);
    });
});

describe("encodeLinkKey", () => {
    it("escapes - and ~ as well as everything encodeURIComponent escapes", () => {
        expect(encodeLinkKey("a-b~c")).toBe("a%2Db%7Ec");
        expect(encodeLinkKey("ä ö/#?%")).toBe(encodeURIComponent("ä ö/#?%"));
        expect(encodeLinkKey("-~")).not.toMatch(/[-~]/);
    });

    it("round-trips through decodeURIComponent", () => {
        for (let p of ["plain", "a-b~c", "❤️ ä/?#&=+%-~_.!*'()"])
            expect(decodeURIComponent(encodeLinkKey(p))).toBe(p);
    });
});

describe("buildShareLink", () => {
    it("is #id without a password", () => {
        expect(buildShareLink("https://x.test", "abc123")).toBe("https://x.test/#abc123");
    });

    it("is #id~key with a password", () => {
        expect(buildShareLink("https://x.test", "abc123", "p-w~1 ä")).toBe("https://x.test/#abc123~p%2Dw%7E1%20%C3%A4");
    });
});

describe("confirmsEncryption", () => {
    it("is true only for an explicit encrypted: true", () => {
        expect(confirmsEncryption({id: "a", encrypted: true})).toBe(true);
        expect(confirmsEncryption({id: "a"})).toBe(false);
        expect(confirmsEncryption({encrypted: "true"})).toBe(false);
        expect(confirmsEncryption(null)).toBe(false);
    });
});

import {isShortPassword, isWellFormed} from "../src/encryption";

describe("password checks", () => {
    it("rejects lone surrogates but accepts pairs", () => {
        expect(isValidPassword("ab\uD800")).toBe(false);
        expect(isValidPassword("\uDC00ab")).toBe(false);
        expect(isValidPassword("a😀b")).toBe(true);
        expect(isWellFormed("\uD83Dx")).toBe(false);
        expect(() => encodeLinkKey("a😀")).not.toThrow();
    });

    it("flags passwords under 12 characters as short", () => {
        expect(isShortPassword("12345678901")).toBe(true);
        expect(isShortPassword("123456789012")).toBe(false);
        expect(isShortPassword(generatePassword())).toBe(false);
    });
});
