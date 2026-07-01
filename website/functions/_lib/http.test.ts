import { describe, it, expect } from "vitest";
import {
  json,
  jsonError,
  badRequest,
  notFound,
  readJson,
  normalizeExpiresIn,
  readStringField,
} from "./http";

describe("json / jsonError", () => {
  it("json sets status, content-type and no-store", async () => {
    const res = json(200, { ok: true });
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("application/json");
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.json()).toEqual({ ok: true });
  });

  it("jsonError produces the {error:{code,message}} envelope", async () => {
    const res = jsonError(418, "TEAPOT", "short and stout");
    expect(res.status).toBe(418);
    expect(await res.json()).toEqual({ error: { code: "TEAPOT", message: "short and stout" } });
  });

  it("badRequest and notFound use expected codes/status", async () => {
    const br = badRequest("bad");
    expect(br.status).toBe(400);
    expect((await br.json<{ error: { code: string } }>()).error.code).toBe("BAD_REQUEST");
    const nf = notFound();
    expect(nf.status).toBe(404);
  });
});

describe("readJson", () => {
  it("parses a valid JSON body", async () => {
    const req = new Request("https://x/", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ a: 1 }),
    });
    expect(await readJson(req)).toEqual({ a: 1 });
  });

  it("returns null for non-JSON content type", async () => {
    const req = new Request("https://x/", {
      method: "POST",
      headers: { "content-type": "text/plain" },
      body: "hello",
    });
    expect(await readJson(req)).toBeNull();
  });

  it("returns null for malformed JSON", async () => {
    const req = new Request("https://x/", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "{not json",
    });
    expect(await readJson(req)).toBeNull();
  });

  it("returns null for oversized body", async () => {
    const req = new Request("https://x/", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ big: "x".repeat(20000) }),
    });
    expect(await readJson(req, 8 * 1024)).toBeNull();
  });
});

describe("normalizeExpiresIn", () => {
  it("returns positive integers as-is (floored)", () => {
    expect(normalizeExpiresIn(3600)).toBe(3600);
    expect(normalizeExpiresIn(1800.9)).toBe(1800);
    expect(normalizeExpiresIn("900")).toBe(900);
  });
  it("falls back for missing / zero / negative / non-numeric", () => {
    expect(normalizeExpiresIn(undefined)).toBe(300);
    expect(normalizeExpiresIn(0)).toBe(300);
    expect(normalizeExpiresIn(-5)).toBe(300);
    expect(normalizeExpiresIn("abc")).toBe(300);
    expect(normalizeExpiresIn(null)).toBe(300);
  });
  it("honors a custom fallback", () => {
    expect(normalizeExpiresIn(0, 600)).toBe(600);
  });
});

describe("readStringField", () => {
  it("returns valid string fields", () => {
    expect(readStringField({ x: "hello" }, "x")).toBe("hello");
  });
  it("returns null for missing / empty / non-string / oversized / null body", () => {
    expect(readStringField({}, "x")).toBeNull();
    expect(readStringField({ x: "" }, "x")).toBeNull();
    expect(readStringField({ x: 5 }, "x")).toBeNull();
    expect(readStringField({ x: "y".repeat(5000) }, "x", 4096)).toBeNull();
    expect(readStringField(null, "x")).toBeNull();
  });
});
