import { argon2id } from 'hash-wasm';

/**
 * SimpleTextCrypt 5.x compatible encryption.
 *
 * Format: the IV (12 random bytes) serves as both the AES-GCM nonce and the
 * Argon2id salt. The key is derived with Argon2id (t=5, m=64 MiB, p=2, 32 bytes,
 * version 0x13) and the text is encrypted with AES-256-GCM with a 128-bit tag and no AAD.
 * The text form matches Android's Base64.DEFAULT: Base64(IV) ‖ '\n' ‖ Base64(ciphertext‖tag)
 * wrapped every 76 characters ‖ '\n'.
 */

export type CrypterErrorCode = 'invalid-format' | 'decrypt-failed' | 'empty-password';

export class CrypterError extends Error {
  readonly code: CrypterErrorCode;

  constructor(code: CrypterErrorCode, message: string) {
    super(message);
    this.name = 'CrypterError';
    this.code = code;
  }
}

const IV_LENGTH = 12;
const TAG_LENGTH = 16;

/** IV 12 + GCM tag 16. */
export const PACKET_OVERHEAD = IV_LENGTH + TAG_LENGTH;

/**
 * Identity helper kept from the reference implementation (where it narrows the buffer type for newer
 * TS libs); this repo's older TypeScript needs no cast.
 */
function view(bytes: Uint8Array): Uint8Array {
  return bytes;
}

async function deriveKey(
  password: string,
  iv: Uint8Array,
  usage: KeyUsage,
): Promise<CryptoKey> {
  if (password.length === 0) {
    throw new CrypterError('empty-password', 'Password must not be empty');
  }
  const raw = await argon2id({
    password,
    salt: iv,
    iterations: 5,
    memorySize: 65536,
    parallelism: 2,
    hashLength: 32,
    outputType: 'binary',
  });
  return crypto.subtle.importKey('raw', view(raw), 'AES-GCM', false, [usage]);
}

function bytesToBase64(bytes: Uint8Array): string {
  // Chunked, because String.fromCharCode(...large) overflows the call stack.
  let binary = '';
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

function base64ToBytes(base64: string): Uint8Array {
  let binary: string;
  try {
    binary = atob(base64);
  } catch {
    throw new CrypterError('invalid-format', 'Message is not valid Base64');
  }
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** Android's Base64.DEFAULT: wrapped every 76 characters, with a trailing newline. */
function androidBase64(bytes: Uint8Array): string {
  const base64 = bytesToBase64(bytes);
  let out = '';
  for (let i = 0; i < base64.length; i += 76) out += base64.slice(i, i + 76) + '\n';
  return out;
}

/**
 * Encrypts bytes and returns the packet IV(12) ‖ ciphertext ‖ tag(16).
 *
 * `iv` is intended ONLY for deterministic testing – production calls must
 * omit it, which uses a cryptographically random IV.
 */
export async function encryptBytes(
  password: string,
  plaintext: Uint8Array,
  iv?: Uint8Array,
): Promise<Uint8Array> {
  if (iv !== undefined && iv.length !== IV_LENGTH) {
    throw new CrypterError('invalid-format', `IV must be exactly ${IV_LENGTH} bytes`);
  }
  const nonce = iv ?? crypto.getRandomValues(new Uint8Array(IV_LENGTH));
  const key = await deriveKey(password, nonce, 'encrypt');
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: view(nonce), tagLength: 128 }, key, view(plaintext)),
  );
  const packet = new Uint8Array(nonce.length + ciphertext.length);
  packet.set(nonce);
  packet.set(ciphertext, nonce.length);
  return packet;
}

/** Decrypts the packet IV(12) ‖ ciphertext ‖ tag(16). */
export async function decryptBytes(password: string, packet: Uint8Array): Promise<Uint8Array> {
  if (packet.length < PACKET_OVERHEAD) {
    throw new CrypterError('invalid-format', `Packet must be at least ${PACKET_OVERHEAD} bytes`);
  }
  const iv = packet.subarray(0, IV_LENGTH);
  const ciphertext = packet.subarray(IV_LENGTH);
  const key = await deriveKey(password, iv, 'decrypt');
  try {
    return new Uint8Array(
      await crypto.subtle.decrypt({ name: 'AES-GCM', iv: view(iv), tagLength: 128 }, key, view(ciphertext)),
    );
  } catch {
    throw new CrypterError('decrypt-failed', 'Wrong password or corrupted message');
  }
}

/** Packet -> Android-compatible text form. */
export function encodeMessage(packet: Uint8Array): string {
  return androidBase64(packet.subarray(0, IV_LENGTH)) + androidBase64(packet.subarray(IV_LENGTH));
}

/**
 * Android-compatible text form -> packet. All whitespace is stripped first,
 * because messages pass through chat apps where newlines get lost or changed.
 */
export function decodeMessage(message: string): Uint8Array {
  const compact = message.replace(/\s+/g, '');
  if (compact.length <= 16) {
    throw new CrypterError('invalid-format', 'Message is too short');
  }
  const iv = base64ToBytes(compact.slice(0, 16));
  const ciphertext = base64ToBytes(compact.slice(16));
  if (iv.length !== IV_LENGTH || ciphertext.length < TAG_LENGTH) {
    throw new CrypterError('invalid-format', 'Message is too short');
  }
  const packet = new Uint8Array(iv.length + ciphertext.length);
  packet.set(iv);
  packet.set(ciphertext, iv.length);
  return packet;
}

export async function encrypt(password: string, text: string): Promise<string> {
  return encodeMessage(await encryptBytes(password, new TextEncoder().encode(text)));
}

export async function decrypt(password: string, message: string): Promise<string> {
  const plaintext = await decryptBytes(password, decodeMessage(message));
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(plaintext);
  } catch {
    throw new CrypterError('decrypt-failed', 'Decrypted data is not valid UTF-8');
  }
}
