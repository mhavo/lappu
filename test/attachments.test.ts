import {describe, expect, it} from "vitest";
import {escapeAlt, rewriteAttachments, Resolver, sha256Hex} from "../src/attachments";
import {attachmentEndpoint, parseMissing} from "../src/http";
import {buildShareBody, buildUpdateBody} from "../src/expiry";

const A = new Uint8Array([1, 2, 3]);
const B = new Uint8Array([4, 5, 6]);

const files: Record<string, { bytes: Uint8Array, ext: string }> = {
    "a.png": {bytes: A, ext: "png"},
    "b.JPG": {bytes: B, ext: "JPG"},
    "my pic.png": {bytes: A, ext: "png"},
    "100%.png": {bytes: B, ext: "png"},
    "sub/c.webp": {bytes: B, ext: "webp"},
    "logo.svg": {bytes: A, ext: "svg"},
    "Note": {bytes: A, ext: "md"},
    "doc.pdf": {bytes: A, ext: "pdf"}
};
const resolve: Resolver = link => files[link] ?? null;

async function hash(b: Uint8Array, ext: string) {
    return `${await sha256Hex(b)}.${ext}`;
}

describe("rewriteAttachments", () => {
    it("handles several images on one line", async () => {
        let r = await rewriteAttachments("![x](a.png) and ![[b.JPG]] ![y](a.png)", resolve);
        let a = await hash(A, "png"), b = await hash(B, "jpg");
        expect(r.markdown).toBe(`![x](lappu-att:${a}) and ![b](lappu-att:${b}) ![y](lappu-att:${a})`);
        expect([...r.attachments.keys()]).toEqual([a, b]);
        expect(r.skipped).toEqual([]);
    });

    it("strips size, alias and heading from wikilinks", async () => {
        let a = await hash(A, "png");
        let r = await rewriteAttachments("![[a.png|300]] ![[a.png|300x200]] ![[a.png|A cat]] ![[a.png#x]] ![[a.png#^blk|Alias]]", resolve);
        expect(r.markdown).toBe([
            `![a](lappu-att:${a})`, `![a](lappu-att:${a})`, `![A cat](lappu-att:${a})`, `![a](lappu-att:${a})`, `![Alias](lappu-att:${a})`
        ].join(" "));
        expect(r.attachments.size).toBe(1);
    });

    it("handles a table-escaped alias pipe", async () => {
        let a = await hash(A, "png");
        expect((await rewriteAttachments("![[a.png\\|Cat]]", resolve)).markdown).toBe(`![Cat](lappu-att:${a})`);
    });

    it("handles titles, angle brackets, spaces and percent encoding", async () => {
        let a = await hash(A, "png"), b = await hash(B, "png");
        let r = await rewriteAttachments('![t](a.png "Title") ![u](a.png \'T\') ![s](<my pic.png>) ![e](my%20pic.png) ![p](100%.png) ![q](100%25.png)', resolve);
        expect(r.markdown).toBe(`![t](lappu-att:${a}) ![u](lappu-att:${a}) ![s](lappu-att:${a}) ![e](lappu-att:${a}) ![p](lappu-att:${b}) ![q](lappu-att:${b})`);
        expect(r.skipped).toEqual([]);
    });

    it("leaves remote and data images untouched", async () => {
        let md = "![a](https://x.com/a.png) ![b](http://x/b.png) ![c](data:image/png;base64,AAAA) ![[a.png]]";
        let r = await rewriteAttachments(md, resolve);
        expect(r.markdown.startsWith(md.slice(0, md.indexOf("![[")))).toBe(true);
        expect(r.attachments.size).toBe(1);
    });

    it("does not treat local names starting with http as remote", async () => {
        let r = await rewriteAttachments("![](httpfoo.png)", name => name === "httpfoo.png" ? {bytes: A, ext: "png"} : null);
        expect(r.attachments.size).toBe(1);
    });

    it("skips non-images and missing files, leaving them in place", async () => {
        let md = "![[logo.svg]] ![[Note]] ![[doc.pdf]] ![](nope.png) ![[logo.svg]]";
        let r = await rewriteAttachments(md, resolve);
        expect(r.markdown).toBe(md);
        expect(r.skipped).toEqual(["logo.svg", "Note", "doc.pdf", "nope.png"]);
        expect(r.attachments.size).toBe(0);
    });

    it("treats a throwing resolver as a missing file", async () => {
        let r = await rewriteAttachments("![](a.png)", () => { throw new Error("boom"); });
        expect(r.skipped).toEqual(["a.png"]);
    });

    it("dedupes identical bytes under different names", async () => {
        let r = await rewriteAttachments("![[a.png]] ![](<my pic.png>)", resolve);
        expect(r.attachments.size).toBe(1);
    });

    it("escapes alt text from wikilinks", async () => {
        expect(escapeAlt("a [b] \\ c\nd")).toBe("a \\[b\\] \\\\ c d");
        let a = await hash(A, "png");
        expect((await rewriteAttachments("![[a.png|x\\y]]", resolve)).markdown).toBe(`![x\\\\y](lappu-att:${a})`);
    });

    it("leaves code untouched", async () => {
        let md = "```md\n![a](a.png)\n![[a.png]]\n```\n\n~~~\n![[a.png]]\n~~~\n\nUse `![a](a.png)` and ``![[a.png]]`` here.\n\n![r](a.png)";
        let r = await rewriteAttachments(md, resolve);
        let a = await hash(A, "png");
        expect(r.markdown).toBe(md.replace(/!\[r]\(a\.png\)$/, `![r](lappu-att:${a})`));
        expect(r.attachments.size).toBe(1);
    });

    it("ignores images inside HTML comments", async () => {
        let a = await hash(A, "png");
        for (let md of ["<!-- ![[a.png]] -->", "<!--\n![x](a.png)\n![[b.JPG]]\n-->", "<!-- ![[a.png]]\nunclosed ![[b.JPG]]"]) {
            let r = await rewriteAttachments(md, resolve);
            expect(r.markdown).toBe(md);
            expect(r.attachments.size).toBe(0);
            expect(r.skipped).toEqual([]);
        }
        let r = await rewriteAttachments("<!-- ![[a.png]] --> ![[a.png]]", resolve);
        expect(r.markdown).toBe(`<!-- ![[a.png]] --> ![a](lappu-att:${a})`);
        expect(r.attachments.size).toBe(1);
    });

    it("does not start a comment inside code", async () => {
        let a = await hash(A, "png");
        let r = await rewriteAttachments("```\n<!--\n```\n![[a.png]]\n\n`<!--` ![[a.png]]", resolve);
        expect(r.markdown).toBe(`\`\`\`\n<!--\n\`\`\`\n![a](lappu-att:${a})\n\n\`<!--\` ![a](lappu-att:${a})`);
    });

    it("treats an unclosed fence as running to the end", async () => {
        let md = "![[a.png]]\n```\n![[a.png]]";
        let r = await rewriteAttachments(md, resolve);
        expect(r.markdown.endsWith("```\n![[a.png]]")).toBe(true);
        expect(r.markdown.startsWith("![a](lappu-att:")).toBe(true);
    });

    it("lets a custom store name and withhold bytes", async () => {
        let r = await rewriteAttachments("![[a.png]]", resolve, async (_b, ext) => ({name: `fixed.${ext}`, bytes: null}));
        expect(r.markdown).toBe("![a](lappu-att:fixed.png)");
        expect(r.attachments.get("fixed.png")).toBeNull();
    });
});

describe("attachment protocol helpers", () => {
    it("builds upload urls and parses missing", () => {
        expect(attachmentEndpoint("https://s.example/", "ab cd", "x.png")).toBe("https://s.example/share.php?att=ab%20cd/x.png");
        expect(parseMissing({missing: ["a", 1, "b"]})).toEqual(["a", "b"]);
        expect(parseMissing({})).toBeNull();
        expect(parseMissing(null)).toBeNull();
    });

    it("adds attachments to bodies", () => {
        expect(JSON.parse(buildShareBody("x", "never", false, ["a.png", "a.png"]))).toEqual({content: "x", attachments: ["a.png"]});
        expect(JSON.parse(buildShareBody("x", "never"))).toEqual({content: "x"});
        expect(JSON.parse(buildUpdateBody("x", false, []))).toEqual({content: "x", attachments: []});
        expect(JSON.parse(buildUpdateBody("x"))).toEqual({content: "x"});
    });
});
