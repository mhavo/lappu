// Finds local images in markdown and rewrites them to `lappu-att:<name>` references.
// Must not import "obsidian" so it stays unit-testable.

/** Image types the server accepts as attachments. */
export const ATTACHMENT_EXTENSIONS: readonly string[] = ["png", "jpg", "jpeg", "gif", "webp", "avif"];

export const ATTACHMENT_SCHEME = "lappu-att:";

export interface ResolvedFile {
    bytes: Uint8Array;
    /** File extension without the dot, any case. */
    ext: string;
}

/** Looks up a link target (as written in the note, without size or heading) and returns the file, or null. */
export type Resolver = (link: string) => Promise<ResolvedFile | null> | ResolvedFile | null;

export interface StoredAttachment {
    name: string;
    /** Bytes to upload; null when the file is known to be on the server already. */
    bytes: Uint8Array | null;
}

/** Turns an image's bytes into the attachment to upload; the default names it by the sha256 of the bytes. */
export type Store = (bytes: Uint8Array, ext: string) => Promise<StoredAttachment>;

export interface RewriteResult {
    markdown: string;
    /** Attachment name to bytes, in order of first use; null bytes mean "already uploaded". */
    attachments: Map<string, Uint8Array | null>;
    /** Attachment name to the link it was first referenced by, for messages. */
    labels: Map<string, string>;
    /** Embeds left untouched in the markdown (not images, or not found), deduplicated. */
    skipped: string[];
}

export function isAttachmentExtension(ext: string): boolean {
    return ATTACHMENT_EXTENSIONS.includes(ext.toLowerCase());
}

export async function sha256Hex(bytes: Uint8Array): Promise<string> {
    let digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes as unknown as BufferSource));
    return Array.from(digest, b => b.toString(16).padStart(2, "0")).join("");
}

export const defaultStore: Store = async (bytes, ext) => ({name: `${await sha256Hex(bytes)}.${ext}`, bytes});

/** Escapes text for use as markdown image alt text. */
export function escapeAlt(text: string): string {
    return text.replace(/\s*\n\s*/g, " ").replace(/[\\\[\]]/g, "\\$&");
}

/** Ranges of the text that must not be touched: fenced code blocks and inline code spans. */
function protectedRanges(text: string): Array<[number, number]> {
    let ranges: Array<[number, number]> = [];
    let fenced: Array<[number, number]> = [];
    let pos = 0;
    let open: { start: number, char: string, length: number } | null = null;
    for (let line of text.split("\n")) {
        let end = pos + line.length;
        let fence = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line.replace(/\r$/, ""));
        if (!open) {
            if (fence && !(fence[1][0] === "`" && fence[2].includes("`")))
                open = {start: pos, char: fence[1][0], length: fence[1].length};
        } else if (fence && fence[1][0] === open.char && fence[1].length >= open.length && fence[2].trim() === "") {
            fenced.push([open.start, end]);
            open = null;
        }
        pos = end + 1;
    }
    if (open)
        fenced.push([open.start, text.length]);
    ranges.push(...fenced);

    // inline code and HTML comments (invisible in the note, so never shared) between fenced blocks
    let from = 0;
    for (let [start, end] of [...fenced, [text.length, text.length] as [number, number]]) {
        let segment = text.substring(from, start);
        let inline = /(`+)(?!`)[\s\S]*?[^`]\1(?!`)|<!--[\s\S]*?(?:-->|$)/g;
        let m: RegExpExecArray | null;
        while ((m = inline.exec(segment)) != null)
            ranges.push([from + m.index, from + m.index + m[0].length]);
        from = end;
    }
    return ranges.sort((a, b) => a[0] - b[0]);
}

const IMAGE_SYNTAX = new RegExp(
    // ![alt](dest "title") with dest either <...> or without spaces (one level of parentheses allowed)
    String.raw`!\[((?:[^\[\]\\\n]|\\.)*)\]\(\s*(?:<([^<>\n]*)>|((?:[^\s()<]|\([^\s()]*\))+))(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)` +
    // ![[target|alias]]
    String.raw`|!\[\[([^\[\]\n]+)\]\]`, "g");

function isRemote(link: string): boolean {
    return /^(https?:\/\/|data:)/i.test(link);
}

function extensionOf(link: string): string | null {
    let m = /\.([A-Za-z0-9]+)$/.exec(link);
    return m ? m[1].toLowerCase() : null;
}

function tryDecode(value: string): string {
    try {
        return decodeURIComponent(value);
    } catch (e) {
        return value;
    }
}

function basenameOf(link: string): string {
    let file = link.split("/").pop() ?? link;
    return file.replace(/\.[A-Za-z0-9]+$/, "");
}

/**
 * Replaces local images with `![alt](lappu-att:<name>)`. Remote and data images and code are
 * left alone; embeds that are not images (or cannot be found) stay as they are and are reported
 * in `skipped`.
 */
export async function rewriteAttachments(markdown: string, resolve: Resolver, store: Store = defaultStore): Promise<RewriteResult> {
    let attachments = new Map<string, Uint8Array | null>();
    let labels = new Map<string, string>();
    let skipped: string[] = [];
    let skip = (label: string) => {
        if (!skipped.includes(label))
            skipped.push(label);
    };

    let rewrite = async (text: string): Promise<string> => {
        let out = "";
        let last = 0;
        let m: RegExpExecArray | null;
        IMAGE_SYNTAX.lastIndex = 0;
        let matches: RegExpExecArray[] = [];
        while ((m = IMAGE_SYNTAX.exec(text)) != null)
            matches.push(m);
        for (let match of matches) {
            let replacement: string | null = null;
            if (match[4] !== undefined) {
                let [target, ...rest] = match[4].split(/\\?\|/);
                let alias = rest.join("|").trim();
                let link = target.split(/[#^]/)[0].trim();
                let alt = alias && !/^\d+(x\d+)?$/.test(alias) ? alias : basenameOf(link);
                replacement = await convert([link], escapeAlt(alt), link);
            } else {
                let raw = match[2] ?? match[3];
                if (!isRemote(raw)) {
                    let candidates = [tryDecode(raw), raw];
                    candidates.push(...candidates.map(c => c.split(/[#?]/)[0]));
                    replacement = await convert([...new Set(candidates)], match[1], tryDecode(raw));
                }
            }
            if (replacement !== null) {
                out += text.substring(last, match.index) + replacement;
                last = match.index + match[0].length;
            }
        }
        return out + text.substring(last);
    };

    let convert = async (candidates: string[], alt: string, label: string): Promise<string | null> => {
        for (let link of candidates) {
            let ext = extensionOf(link);
            if (ext !== null && !isAttachmentExtension(ext))
                continue;
            let file: ResolvedFile | null = null;
            try {
                file = await resolve(link);
            } catch (e) {
                file = null;
            }
            if (!file)
                continue;
            let fileExt = file.ext.toLowerCase();
            if (!isAttachmentExtension(fileExt)) {
                skip(label);
                return null;
            }
            let stored = await store(file.bytes, fileExt);
            if (!attachments.has(stored.name)) {
                attachments.set(stored.name, stored.bytes);
                labels.set(stored.name, label);
            }
            return `![${alt}](${ATTACHMENT_SCHEME}${stored.name})`;
        }
        skip(label);
        return null;
    };

    let result = "";
    let from = 0;
    for (let [start, end] of protectedRanges(markdown)) {
        if (start < from)
            continue;
        result += await rewrite(markdown.substring(from, start)) + markdown.substring(start, end);
        from = end;
    }
    result += await rewrite(markdown.substring(from));
    return {markdown: result, attachments, labels, skipped};
}
