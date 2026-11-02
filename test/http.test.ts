import {describe, expect, it} from "vitest";
import {buildHeaders, classifyResponse, isConfigured, shareEndpoint} from "../src/http";

describe("classifyResponse", () => {
    it("accepts 2xx", () => {
        expect(classifyResponse(200, "{}", "share")).toBe("ok");
        expect(classifyResponse(204, "", "delete")).toBe("ok");
    });

    it("distinguishes a bad token from a bad share password", () => {
        expect(classifyResponse(401, "Invalid token", "share")).toBe("invalidToken");
        expect(classifyResponse(401, "Invalid token\n", "update")).toBe("invalidToken");
        expect(classifyResponse(401, "Unauthorized", "update")).toBe("wrongPassword");
        expect(classifyResponse(401, "", "delete")).toBe("wrongPassword");
    });

    it("treats a 401 without a share password as a token problem", () => {
        // POST carries no share password, so any 401 must be the token.
        expect(classifyResponse(401, "Unauthorized", "share")).toBe("invalidToken");
    });

    it("reports a missing share only for update and delete", () => {
        expect(classifyResponse(404, "Not found", "update")).toBe("notFound");
        expect(classifyResponse(404, "Not found", "delete")).toBe("notFound");
        expect(classifyResponse(404, "Not found", "share")).toBe("other");
    });

    it("recognises size and rate limits", () => {
        expect(classifyResponse(413, "Too large", "share")).toBe("tooLarge");
        expect(classifyResponse(429, "Too many requests", "delete")).toBe("rateLimited");
    });

    it("falls back to other", () => {
        expect(classifyResponse(500, "boom", "share")).toBe("other");
        expect(classifyResponse(400, "bad", "update")).toBe("other");
    });
});

describe("buildHeaders", () => {
    it("sends the bearer token", () => {
        expect(buildHeaders("abc")).toEqual({"Authorization": "Bearer abc"});
    });

    it("adds the share password when given", () => {
        expect(buildHeaders("abc", "pw")).toEqual({"Authorization": "Bearer abc", "Password": "pw"});
    });

    it("trims whitespace around the token", () => {
        expect(buildHeaders("  abc \n")).toEqual({"Authorization": "Bearer abc"});
    });
});

describe("isConfigured", () => {
    it("requires both url and token", () => {
        expect(isConfigured({url: "https://x", token: "t"})).toBe(true);
        expect(isConfigured({url: "", token: "t"})).toBe(false);
        expect(isConfigured({url: "https://x", token: "  "})).toBe(false);
        expect(isConfigured({url: " ", token: "t"})).toBe(false);
    });
});

describe("shareEndpoint", () => {
    it("joins the base url without double slashes", () => {
        expect(shareEndpoint("https://x")).toBe("https://x/share.php");
        expect(shareEndpoint("https://x/")).toBe("https://x/share.php");
        expect(shareEndpoint(" https://x/sub// ")).toBe("https://x/sub/share.php");
    });

    it("appends an encoded id", () => {
        expect(shareEndpoint("https://x", "ab12")).toBe("https://x/share.php?id=ab12");
        expect(shareEndpoint("https://x", "a&b")).toBe("https://x/share.php?id=a%26b");
    });
});

import {metaEndpoint} from "../src/http";

describe("metaEndpoint", () => {
    it("builds the public meta url", () => {
        expect(metaEndpoint("https://x.test/", "abc")).toBe("https://x.test/share.php?meta&id=abc");
        expect(metaEndpoint(" https://x.test ", "a b&c")).toBe("https://x.test/share.php?meta&id=a%20b%26c");
    });
});
