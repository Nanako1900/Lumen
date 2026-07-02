import { describe, it, expect } from "vitest";
import { isLoopbackRedirectUri, safeUrl } from "../../functions/_lib/loopback";

describe("isLoopbackRedirectUri", () => {
  it("accepts http://127.0.0.1:<port>/... with any port", () => {
    expect(isLoopbackRedirectUri("http://127.0.0.1:8931/cb")).toBe(true);
    expect(isLoopbackRedirectUri("http://127.0.0.1:1/callback")).toBe(true);
    expect(isLoopbackRedirectUri("http://127.0.0.1:65535/x")).toBe(true);
  });

  it("rejects localhost hostname (DNS rebinding defense)", () => {
    expect(isLoopbackRedirectUri("http://localhost:8931/cb")).toBe(false);
  });

  it("rejects non-http scheme", () => {
    expect(isLoopbackRedirectUri("https://127.0.0.1:8931/cb")).toBe(false);
  });

  it("rejects non-loopback hosts", () => {
    expect(isLoopbackRedirectUri("http://192.168.1.5:8931/cb")).toBe(false);
    expect(isLoopbackRedirectUri("http://example.com/cb")).toBe(false);
    expect(isLoopbackRedirectUri("http://0.0.0.0:8931/cb")).toBe(false);
    // IPv6 loopback [::1] is intentionally not accepted by the 127.0.0.1 rule
    expect(isLoopbackRedirectUri("http://[::1]:8931/cb")).toBe(false);
  });

  it("rejects malformed / relative / empty URIs", () => {
    expect(isLoopbackRedirectUri("")).toBe(false);
    expect(isLoopbackRedirectUri("/cb")).toBe(false);
    expect(isLoopbackRedirectUri("127.0.0.1:8931/cb")).toBe(false);
    expect(isLoopbackRedirectUri("not a url")).toBe(false);
  });
});

describe("safeUrl", () => {
  it("returns URL for valid input and null for invalid", () => {
    expect(safeUrl("http://127.0.0.1:8931/cb")?.hostname).toBe("127.0.0.1");
    expect(safeUrl("garbage")).toBeNull();
    expect(safeUrl("")).toBeNull();
  });
});
