import { describe, it, expect } from "vitest";
import {
  sealSession,
  openSession,
  buildSessionCookie,
  clearSessionCookie,
  readSessionCookie,
  defaultSessionExp,
  SESSION_COOKIE,
} from "../../functions/_lib/session";
import { makeEnv } from "./testutil";

const testEnv = makeEnv();

describe("web session cookie (AES-GCM)", () => {
  it("round-trips a sealed session", async () => {
    const sealed = await sealSession(testEnv, {
      sub: "user-1",
      display_name: "Alice",
      avatar_url: "https://img/a.png",
      exp: defaultSessionExp(),
    });
    const opened = await openSession(testEnv, sealed);
    expect(opened).not.toBeNull();
    expect(opened!.sub).toBe("user-1");
    expect(opened!.display_name).toBe("Alice");
    expect(opened!.avatar_url).toBe("https://img/a.png");
  });

  it("rejects a tampered ciphertext", async () => {
    const sealed = await sealSession(testEnv, {
      sub: "u",
      display_name: "d",
      avatar_url: "a",
      exp: defaultSessionExp(),
    });
    // flip a char in the ciphertext part
    const [iv, ct] = sealed.split(".");
    const tampered = `${iv}.${ct.slice(0, -1)}${ct.slice(-1) === "A" ? "B" : "A"}`;
    expect(await openSession(testEnv, tampered)).toBeNull();
  });

  it("rejects an expired session", async () => {
    const sealed = await sealSession(testEnv, {
      sub: "u",
      display_name: "d",
      avatar_url: "a",
      exp: Math.floor(Date.now() / 1000) - 10, // already expired
    });
    expect(await openSession(testEnv, sealed)).toBeNull();
  });

  it("rejects malformed cookie values", async () => {
    expect(await openSession(testEnv, "garbage")).toBeNull();
    expect(await openSession(testEnv, "a.b")).toBeNull();
    expect(await openSession(testEnv, "")).toBeNull();
  });

  it("cookie contains HttpOnly, Secure, SameSite=Lax", () => {
    const c = buildSessionCookie("value123");
    expect(c).toContain(`${SESSION_COOKIE}=value123`);
    expect(c).toContain("HttpOnly");
    expect(c).toContain("Secure");
    expect(c).toContain("SameSite=Lax");
    expect(c).toContain("Path=/");
  });

  it("clear cookie sets Max-Age=0", () => {
    expect(clearSessionCookie()).toContain("Max-Age=0");
  });

  it("reads the session value from a Cookie header", () => {
    const req = new Request("https://x/", {
      headers: { cookie: `other=1; ${SESSION_COOKIE}=abc.def; more=2` },
    });
    expect(readSessionCookie(req)).toBe("abc.def");
    expect(readSessionCookie(new Request("https://x/"))).toBeNull();
  });
});
