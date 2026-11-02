import {describe, expect, it} from "vitest";
import {sanitizeSharedItem} from "../src/settings";

const base = {id: "a", password: "p", path: "A.md"};

describe("sanitizeSharedItem", () => {
    it("rejects unusable items", () => {
        for (let bad of [null, undefined, 3, "x", {}, {...base, id: ""}, {...base, password: 1}, {...base, path: null}])
            expect(sanitizeSharedItem(bad)).toBeNull();
    });

    it("keeps a plain item minimal", () => {
        expect(sanitizeSharedItem(base)).toEqual(base);
    });

    it("never turns an encrypted item into a plain one", () => {
        for (let flag of [true, "true", 1, {}])
            expect(sanitizeSharedItem({...base, encrypted: flag, encryptionPassword: 7})?.encrypted).toBe(true);
        expect(sanitizeSharedItem({...base, encrypted: true, encryptionPassword: 7})?.encryptionPassword).toBeUndefined();
    });

    it("cleans attachmentCache", () => {
        expect(sanitizeSharedItem({...base, attachmentCache: {a: "n", b: 3}})?.attachmentCache).toEqual({a: "n"});
        expect(sanitizeSharedItem({...base, attachmentCache: []})?.attachmentCache).toBeUndefined();
        expect(sanitizeSharedItem({...base, attachmentCache: "x"})?.attachmentCache).toBeUndefined();
    });
});
