import {describe, expect, it} from "vitest";
import {IMPORT_SOURCES, mergeImported} from "../src/import";
import {defaultSettings, LappuSettings} from "../src/settings";

function settings(overrides: Partial<LappuSettings> = {}): LappuSettings {
    return {...defaultSettings, shared: [], ...overrides};
}

describe("mergeImported", () => {
    it("imports url, shares and boolean settings into empty settings", () => {
        let result = mergeImported(settings(), {
            url: "https://jsp.example.com",
            shared: [{id: "a1", password: "p", path: "A.md"}],
            stripFrontmatter: false,
            includeNoteName: false,
            unshareDeletedFiles: false,
            autoUpdateShares: true
        });
        expect(result.settings.url).toBe("https://jsp.example.com");
        expect(result.settings.shared).toEqual([{id: "a1", password: "p", path: "A.md"}]);
        expect(result.settings.stripFrontmatter).toBe(false);
        expect(result.settings.includeNoteName).toBe(false);
        expect(result.settings.unshareDeletedFiles).toBe(false);
        expect(result.settings.autoUpdateShares).toBe(true);
        expect(result.added).toBe(1);
    });

    it("merges shares and dedupes by id, keeping existing entries", () => {
        let current = settings({shared: [{id: "a1", password: "mine", path: "Renamed.md"}]});
        let result = mergeImported(current, {
            shared: [
                {id: "a1", password: "old", path: "A.md"},
                {id: "b2", password: "p", path: "B.md"},
                {id: "b2", password: "p", path: "B.md"}
            ]
        });
        expect(result.settings.shared).toEqual([
            {id: "a1", password: "mine", path: "Renamed.md"},
            {id: "b2", password: "p", path: "B.md"}
        ]);
        expect(result.added).toBe(1);
    });

    it("keeps an already configured url", () => {
        let result = mergeImported(settings({url: "https://mine"}), {url: "https://old"});
        expect(result.settings.url).toBe("https://mine");
    });

    it("does not adopt the original public server url", () => {
        let result = mergeImported(settings(), {url: "https://jsp.ellpeck.de"});
        expect(result.settings.url).toBe("");
    });

    it("never touches the token or language from the original plugin", () => {
        let current = settings({token: "secret", language: "fi"});
        let result = mergeImported(current, {token: "x", language: "en"} as never);
        expect(result.settings.token).toBe("secret");
        expect(result.settings.language).toBe("fi");
    });

    it("ignores malformed data", () => {
        let current = settings({shared: [{id: "a1", password: "p", path: "A.md"}]});
        let result = mergeImported(current, {
            url: 42,
            shared: [null, {id: "x"}, {id: 1, password: "p", path: "P.md"}, "nope"],
            stripFrontmatter: "yes"
        } as never);
        expect(result.settings.url).toBe(current.url);
        expect(result.settings.shared).toEqual(current.shared);
        expect(result.settings.stripFrontmatter).toBe(current.stripFrontmatter);
        expect(result.added).toBe(0);

        expect(mergeImported(current, null).settings).toEqual(current);
        expect(mergeImported(current, "garbage" as never).settings).toEqual(current);
    });

    it("does not mutate its inputs", () => {
        let current = settings({shared: [{id: "a1", password: "p", path: "A.md"}]});
        let imported = {shared: [{id: "b2", password: "p", path: "B.md"}]};
        mergeImported(current, imported);
        expect(current.shared).toHaveLength(1);
        expect(imported.shared).toHaveLength(1);
    });

    it("takes the token and language from a trusted source when none are set", () => {
        let result = mergeImported(settings(), {url: "https://previous.example.com", token: "old-token", language: "fi"}, {trusted: true});
        expect(result.settings.url).toBe("https://previous.example.com");
        expect(result.settings.token).toBe("old-token");
        expect(result.settings.language).toBe("fi");
    });

    it("keeps an already configured token and explicit language even from a trusted source", () => {
        let result = mergeImported(settings({token: "mine", language: "en"}), {token: "old", language: "fi"}, {trusted: true});
        expect(result.settings.token).toBe("mine");
        expect(result.settings.language).toBe("en");
    });

    it("ignores malformed token and language from a trusted source", () => {
        let result = mergeImported(settings(), {token: 42, language: "xx"} as never, {trusted: true});
        expect(result.settings.token).toBe("");
        expect(result.settings.language).toBe("auto");
    });

    it("takes a trusted token when the configured url already equals the source url", () => {
        let result = mergeImported(settings({url: "https://previous.example.com"}), {url: " https://previous.example.com ", token: "old"}, {trusted: true});
        expect(result.settings.token).toBe("old");
    });

    it("does not take a trusted token without a url from the same source", () => {
        let result = mergeImported(settings(), {token: "old"}, {trusted: true});
        expect(result.settings.token).toBe("");
    });

    it("does not pair a trusted token with a url configured elsewhere", () => {
        let result = mergeImported(settings({url: "https://manual.example.com"}), {token: "old"}, {trusted: true});
        expect(result.settings.url).toBe("https://manual.example.com");
        expect(result.settings.token).toBe("");

        result = mergeImported(settings({url: "https://manual.example.com"}), {url: "https://previous.example.com", token: "old"}, {trusted: true});
        expect(result.settings.url).toBe("https://manual.example.com");
        expect(result.settings.token).toBe("");
    });

    it("does not take a trusted token when the source url is the rejected public server", () => {
        let result = mergeImported(settings(), {url: "https://jsp.ellpeck.de", token: "old"}, {trusted: true});
        expect(result.settings.url).toBe("");
        expect(result.settings.token).toBe("");
    });

    it("does not let an untrusted source set the url while a token is configured", () => {
        let result = mergeImported(settings({token: "secret"}), {url: "https://evil.example.com"});
        expect(result.settings.url).toBe("");
        expect(result.settings.token).toBe("secret");

        result = mergeImported(settings({token: "  "}), {url: "https://original.example.com"});
        expect(result.settings.url).toBe("https://original.example.com");
    });

    it("still rejects the original public server url from a trusted source", () => {
        let result = mergeImported(settings(), {url: "https://jsp.ellpeck.de/"}, {trusted: true});
        expect(result.settings.url).toBe("");
    });
});

describe("IMPORT_SOURCES", () => {
    it("lists the previous self-hosted plugin first, trusted, and the original plugin untrusted", () => {
        expect(IMPORT_SOURCES).toEqual([
            {id: "just-share-please-selfhosted", trusted: true},
            {id: "just-share-please", trusted: false}
        ]);
    });

    it("lets the previous self-hosted url win when merging sources in order", () => {
        let current = settings();
        for (let [source, data] of [
            [IMPORT_SOURCES[0], {url: "https://previous.example.com", token: "t", shared: [{id: "a1", password: "p", path: "A.md"}]}],
            [IMPORT_SOURCES[1], {url: "https://original.example.com", token: "upstream", shared: [{id: "b2", password: "p", path: "B.md"}]}]
        ] as const)
            current = mergeImported(current, data, {trusted: source.trusted}).settings;
        expect(current.url).toBe("https://previous.example.com");
        expect(current.token).toBe("t");
        expect(current.shared.map(s => s.id)).toEqual(["a1", "b2"]);
    });

    it("never pairs a url-less trusted token with the untrusted source's url", () => {
        let current = settings();
        for (let [source, data] of [
            [IMPORT_SOURCES[0], {token: "t", shared: [{id: "a1", password: "p", path: "A.md"}]}],
            [IMPORT_SOURCES[1], {url: "https://original.example.com", shared: [{id: "b2", password: "p", path: "B.md"}]}]
        ] as const)
            current = mergeImported(current, data, {trusted: source.trusted}).settings;
        expect(current.token).toBe("");
        expect(current.url).toBe("https://original.example.com");
        expect(current.shared.map(s => s.id)).toEqual(["a1", "b2"]);
    });
});

describe("mergeImported encrypted shares", () => {
    it("carries over encryption fields with valid types", () => {
        let result = mergeImported(settings(), {
            shared: [{id: "e1", password: "p", path: "E.md", encrypted: true, encryptionPassword: "secret",
                passwordInLink: false, expires: 1234}]
        });
        expect(result.settings.shared[0]).toEqual({id: "e1", password: "p", path: "E.md", encrypted: true,
            encryptionPassword: "secret", passwordInLink: false, expires: 1234});
    });

    it("drops invalid types but keeps an encrypted item encrypted", () => {
        let result = mergeImported(settings(), {
            shared: [{id: "e1", password: "p", path: "E.md", encrypted: "yes", encryptionPassword: 5,
                passwordInLink: "no", expires: "soon", attachmentCache: "x"}]
        });
        expect(result.settings.shared[0]).toEqual({id: "e1", password: "p", path: "E.md", encrypted: true});
    });
});
