import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  CrypterError,
  PACKET_OVERHEAD,
  decodeMessage,
  decrypt,
  decryptBytes,
  encodeMessage,
  encrypt,
  encryptBytes,
} from '../src/crypter';

const vectors: Array<{ password: string; plaintext: string; ivHex: string; message: string }> =
  JSON.parse(readFileSync(join(__dirname, 'vectors.json'), 'utf-8'));

describe('decrypt (vectors)', () => {
  for (const [i, v] of vectors.entries()) {
    it(`decrypts vector ${i}`, { timeout: 30_000 }, async () => {
      expect(await decrypt(v.password, v.message)).toBe(v.plaintext);
    });
  }
});

function hexToBytes(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

describe('encrypt (vectors)', () => {
  for (const [i, v] of vectors.entries()) {
    it(`produces vector ${i}'s text form exactly`, { timeout: 30_000 }, async () => {
      const packet = await encryptBytes(v.password, new TextEncoder().encode(v.plaintext), hexToBytes(v.ivHex));
      expect(encodeMessage(packet)).toBe(v.message);
    });
  }
});

describe('whitespace tolerance', () => {
  const v = vectors[0]!;

  it('decrypts a message without newlines', { timeout: 30_000 }, async () => {
    expect(await decrypt(v.password, v.message.replace(/\n/g, ''))).toBe(v.plaintext);
  });

  it('decrypts a message with extra spaces and CRLFs', { timeout: 30_000 }, async () => {
    const mangled = ' ' + v.message.replace(/\n/g, ' \r\n ') + '\r\n ';
    expect(await decrypt(v.password, mangled)).toBe(v.plaintext);
  });
});

describe('round-trip', () => {
  // Passwords and messages from the original app's CrypterTest.kt, paired so that
  // each occurs at least once (a full cross product would be too slow).
  const cases: Array<[string, string]> = [
    ['uir43@#89djhncAd.,[]-+091jhdncq`', "Hi there! I'm a test data that is going to be encrypted."],
    ['18736', 'I love you! ❤'],
    ['❤️😜🙈🙃🍎🍀🍵🍪', 'یک مسیج فارسی سری که برای بقیه بفرستیم'],
    ['کلید فارسی', "Hi there! I'm a test data that is going to be encrypted."],
  ];

  for (const [password, text] of cases) {
    it(`encrypts and decrypts: ${password.slice(0, 8)}`, { timeout: 60_000 }, async () => {
      expect(await decrypt(password, await encrypt(password, text))).toBe(text);
    });
  }
});

it('produces a different result for the same text (random IV)', { timeout: 60_000 }, async () => {
  const a = await encrypt('salasana', 'sama teksti');
  const b = await encrypt('salasana', 'sama teksti');
  expect(a).not.toBe(b);
});

describe('errors', () => {
  async function codeOf(fn: () => Promise<unknown>): Promise<string> {
    try {
      await fn();
    } catch (error) {
      expect(error).toBeInstanceOf(CrypterError);
      return (error as CrypterError).code;
    }
    throw new Error('expected an error');
  }

  it('wrong password -> decrypt-failed', { timeout: 60_000 }, async () => {
    const message = await encrypt('oikea', 'salaisuus');
    expect(await codeOf(() => decrypt('wrong', message))).toBe('decrypt-failed');
  });

  it('altered ciphertext -> decrypt-failed', { timeout: 60_000 }, async () => {
    const v = vectors[0]!;
    const compact = v.message.replace(/\n/g, '');
    const flipped = compact.slice(0, 20) + (compact[20] === 'A' ? 'B' : 'A') + compact.slice(21);
    expect(await codeOf(() => decrypt(v.password, flipped))).toBe('decrypt-failed');
  });

  for (const junk of ['hello', '', '!!!!']) {
    it(`junk input ${JSON.stringify(junk)} -> invalid-format`, async () => {
      expect(await codeOf(() => decrypt('salasana', junk))).toBe('invalid-format');
    });
  }

  it('empty password -> empty-password', async () => {
    expect(await codeOf(() => encrypt('', 'teksti'))).toBe('empty-password');
    expect(await codeOf(() => decrypt('', vectors[0]!.message))).toBe('empty-password');
  });
});

describe('byte level', () => {
  it('encrypts and decrypts empty text, packet is exactly PACKET_OVERHEAD', { timeout: 60_000 }, async () => {
    const packet = await encryptBytes('salasana', new Uint8Array(0));
    expect(packet.length).toBe(PACKET_OVERHEAD);
    expect(PACKET_OVERHEAD).toBe(28);
    expect(await decryptBytes('salasana', packet)).toEqual(new Uint8Array(0));
    expect(decodeMessage(encodeMessage(packet))).toEqual(packet);
    expect(await decrypt('salasana', encodeMessage(packet))).toBe('');
  });

  it('rejects a wrong-length IV', async () => {
    await expect(encryptBytes('salasana', new Uint8Array(1), new Uint8Array(11))).rejects.toMatchObject({
      code: 'invalid-format',
    });
  });

  it('rejects a too-short packet', async () => {
    await expect(decryptBytes('salasana', new Uint8Array(PACKET_OVERHEAD - 1))).rejects.toBeInstanceOf(
      CrypterError,
    );
  });
});
