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

import {buildExpiryBody, expiryMatches, isGoneReply, parseExpiryReply, partitionExpired} from "../src/expiry";

describe("buildExpiryBody", () => {
    it("sends only expiresIn, null for never", () => {
        expect(JSON.parse(buildExpiryBody("never"))).toEqual({expiresIn: null});
        expect(JSON.parse(buildExpiryBody("day"))).toEqual({expiresIn: 86400});
        expect(JSON.parse(buildExpiryBody("month"))).toEqual({expiresIn: 2592000});
    });

    it("never carries content, encryption or attachments", () => {
        for (let o of EXPIRY_OPTIONS)
            expect(Object.keys(JSON.parse(buildExpiryBody(o)))).toEqual(["expiresIn"]);
    });
});

describe("parseExpiryReply", () => {
    it("returns the new expiry from an updated server", () => {
        expect(parseExpiryReply({id: "a", expires: 1700000000})).toBe(1700000000);
        expect(parseExpiryReply({id: "a", expires: null})).toBeNull();
    });

    it("is undefined when the reply has no expires key (older server)", () => {
        expect(parseExpiryReply({id: "a"})).toBeUndefined();
        expect(parseExpiryReply(null)).toBeUndefined();
        expect(parseExpiryReply("ok")).toBeUndefined();
    });

    it("is undefined for invalid values", () => {
        expect(parseExpiryReply({expires: "1700000000"})).toBeUndefined();
        expect(parseExpiryReply({expires: -5})).toBeUndefined();
        expect(parseExpiryReply({expires: 0})).toBeUndefined();
        expect(parseExpiryReply({expires: undefined})).toBeUndefined();
    });
});

describe("expiryMatches", () => {
    it("pairs never with null and timed options with a time", () => {
        expect(expiryMatches("never", null)).toBe(true);
        expect(expiryMatches("day", 1700000000)).toBe(true);
        expect(expiryMatches("day", null)).toBe(false);
        expect(expiryMatches("never", 1700000000)).toBe(false);
    });
});

describe("partitionExpired", () => {
    let now = 1_000_000 * 1000;
    it("splits by stored expiry; items without one are kept for checking", () => {
        type Item = { id: string, expires?: number | null };
        let past: Item = {id: "p", expires: 999_999};
        let exact: Item = {id: "e", expires: 1_000_000};
        let future: Item = {id: "f", expires: 1_000_001};
        let never: Item = {id: "n", expires: null};
        let legacy: Item = {id: "l"};
        let {expired, remaining} = partitionExpired([past, exact, future, never, legacy], now);
        expect(expired).toEqual([past, exact]);
        expect(remaining).toEqual([future, never, legacy]);
    });

    it("handles an empty list", () => {
        expect(partitionExpired([], now)).toEqual({expired: [], remaining: []});
    });
});

describe("isGoneReply", () => {
    it("is true only for a 404 with the server's JSON error", () => {
        expect(isGoneReply(404, {error: "Not found"})).toBe(true);
    });

    it("keeps items on other statuses", () => {
        for (let s of [200, 204, 400, 401, 403, 410, 429, 500, 502, 0])
            expect(isGoneReply(s, {error: "x"})).toBe(false);
    });

    it("keeps items on a 404 that is not the server's (e.g. wrong URL)", () => {
        expect(isGoneReply(404, null)).toBe(false);
        expect(isGoneReply(404, "Not found")).toBe(false);
        expect(isGoneReply(404, ["error"])).toBe(false);
        expect(isGoneReply(404, {id: "a"})).toBe(false);
    });
});
