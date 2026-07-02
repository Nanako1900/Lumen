import { describe, it, expect } from "vitest";
import {
  base64urlEncode,
  base64urlDecode,
  isBase64Url,
  s256,
  randomToken,
  timingSafeEqual,
} from "../../functions/_lib/pkce";

describe("base64url", () => {
  it("encodes without padding and URL-safe alphabet", () => {
    // 0xFF 0xFE 0xFD → base64 "//79" → base64url "__79"
    expect(base64urlEncode(new Uint8Array([0xff, 0xfe, 0xfd]))).toBe("__79");
  });

  it("round-trips arbitrary bytes", () => {
    const bytes = new Uint8Array([0, 1, 2, 250, 251, 252, 253, 254, 255]);
    const encoded = base64urlEncode(bytes);
    expect(encoded).not.toContain("=");
    expect(encoded).not.toContain("+");
    expect(encoded).not.toContain("/");
    expect(Array.from(base64urlDecode(encoded))).toEqual(Array.from(bytes));
  });

  it("rejects non-base64url on decode", () => {
    expect(() => base64urlDecode("not/valid+base64url=")).toThrow();
  });
});

describe("isBase64Url", () => {
  it("accepts valid url-safe strings", () => {
    expect(isBase64Url("abcABC012_-")).toBe(true);
  });
  it("rejects empty, padded, or non-url-safe", () => {
    expect(isBase64Url("")).toBe(false);
    expect(isBase64Url("has=pad")).toBe(false);
    expect(isBase64Url("has/slash")).toBe(false);
    expect(isBase64Url("has+plus")).toBe(false);
    expect(isBase64Url(123)).toBe(false);
    expect(isBase64Url(null)).toBe(false);
  });
});

describe("s256", () => {
  it("matches the RFC 7636 test vector", async () => {
    // RFC 7636 Appendix B:
    // verifier "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
    // → challenge "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
    const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
    expect(await s256(verifier)).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  });

  it("is deterministic and base64url", async () => {
    const a = await s256("some-verifier-value");
    const b = await s256("some-verifier-value");
    expect(a).toBe(b);
    expect(isBase64Url(a)).toBe(true);
  });
});

describe("randomToken", () => {
  it("produces high-entropy unique base64url tokens", () => {
    const tokens = new Set<string>();
    for (let i = 0; i < 100; i++) {
      const t = randomToken();
      expect(isBase64Url(t)).toBe(true);
      tokens.add(t);
    }
    expect(tokens.size).toBe(100);
  });

  it("honors the byte length argument", () => {
    // 48 bytes → 64 base64url chars (48 * 4 / 3), no padding
    expect(randomToken(48).length).toBe(64);
  });
});

describe("timingSafeEqual", () => {
  it("returns true for equal strings", () => {
    expect(timingSafeEqual("abcdef", "abcdef")).toBe(true);
  });
  it("returns false for differing strings of same length", () => {
    expect(timingSafeEqual("abcdef", "abcdez")).toBe(false);
  });
  it("returns false for differing lengths", () => {
    expect(timingSafeEqual("abc", "abcd")).toBe(false);
  });
});
