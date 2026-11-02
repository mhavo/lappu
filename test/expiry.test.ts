import {describe, expect, it} from "vitest";
import {buildShareBody, buildUpdateBody, EXPIRY_OPTIONS, expiryToSeconds, isExpired, isExpiryOption, parseExpires} from "../src/expiry";
import {translations} from "../src/i18n";

describe("expiryToSeconds", () => {
    it("maps options to seconds, never to undefined", () => {
        expect(expiryToSeconds("never")).toBeUndefined();
        expect(expiryToSeconds("day")).toBe(86400);
        expect(expiryToSeconds("week")).toBe(604800);
        expect(expiryToSeconds("month")).toBe(2592000);
    });

    it("stays within the server limit", () => {
        for (let o of EXPIRY_OPTIONS) {
            let s = expiryToSeconds(o);
            if (s !== undefined) {
                expect(s).toBeGreaterThanOrEqual(1);
                expect(s).toBeLessThanOrEqual(31536000);
            }
        }
    });
});

describe("buildShareBody", () => {
    it("omits expiresIn for never", () => {
        expect(JSON.parse(buildShareBody("hi", "never"))).toEqual({content: "hi"});
    });
    it("includes expiresIn otherwise", () => {
        expect(JSON.parse(buildShareBody("hi", "week"))).toEqual({content: "hi", expiresIn: 604800});
    });
});

describe("parseExpires", () => {
    it("reads unix seconds", () => expect(parseExpires(1800000000)).toBe(1800000000));
    it("treats missing, null and junk as null", () => {
        expect(parseExpires(undefined)).toBeNull();
        expect(parseExpires(null)).toBeNull();
        expect(parseExpires("123")).toBeNull();
        expect(parseExpires(NaN)).toBeNull();
        expect(parseExpires(0)).toBeNull();
    });
});

describe("isExpired", () => {
    it("never expires without a timestamp", () => {
        expect(isExpired(null)).toBe(false);
        expect(isExpired(undefined)).toBe(false);
    });
    it("compares against now", () => {
        expect(isExpired(100, 100_000)).toBe(true);
        expect(isExpired(101, 100_000)).toBe(false);
    });
});

describe("isExpiryOption", () => {
    it("validates", () => {
        expect(isExpiryOption("day")).toBe(true);
        expect(isExpiryOption("year")).toBe(false);
        expect(isExpiryOption(undefined)).toBe(false);
    });
});

describe("expiry translations", () => {
    it("has a label in both languages for every option", () => {
        for (let lang of ["fi", "en"] as const)
            for (let o of EXPIRY_OPTIONS)
                expect(translations[lang][`expiry.${o}`]).toBeTruthy();
        expect(translations.fi["expiry.never"]).toBe("Ei koskaan");
    });
});

describe("encrypted request bodies", () => {
    it("adds encrypted: true to the share body only when encrypting", () => {
        expect(JSON.parse(buildShareBody("c", "week", true))).toEqual({content: "c", expiresIn: 7 * 86400, encrypted: true});
        expect(JSON.parse(buildShareBody("c", "never", true))).toEqual({content: "c", encrypted: true});
        expect(JSON.parse(buildShareBody("c", "never"))).toEqual({content: "c"});
        expect(JSON.parse(buildShareBody("c", "never", false))).toEqual({content: "c"});
    });

    it("builds update bodies with the encrypted flag", () => {
        expect(JSON.parse(buildUpdateBody("c"))).toEqual({content: "c"});
        expect(JSON.parse(buildUpdateBody("c", true))).toEqual({content: "c", encrypted: true});
    });
});
