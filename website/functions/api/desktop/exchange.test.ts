import { describe, it, expect } from "vitest";
import { env } from "cloudflare:test";
import { onRequestPost } from "./exchange";
import { makeContext, jsonPost, fakeJwt } from "../../_lib/testutil";
import { putHandoff, getSession, type HandoffRecord } from "../../_lib/kv";
import { s256, randomToken } from "../../_lib/pkce";
import type { Env } from "../../_lib/env";

const testEnv = env as unknown as Env;
const URL_EXCHANGE = "https://test.example/api/desktop/exchange";

async function seedHandoff(overrides?: Partial<HandoffRecord>): Promise<{
  handoffCode: string;
  verifier: string;
}> {
  const verifier = randomToken();
  const challenge = await s256(verifier);
  const handoffCode = randomToken();
  const record: HandoffRecord = {
    access_token: fakeJwt({ sub: "u-1", aud: "lumen-api" }),
    expires_in: 3600,
    refresh_token: "refresh-token-1",
    sub: "u-1",
    bound_challenge: challenge,
    profile: { display_name: "Alice", avatar_url: "https://img/a.png" },
    ...overrides,
  };
  await putHandoff(testEnv, handoffCode, record);
  return { handoffCode, verifier };
}

describe("POST /api/desktop/exchange", () => {
  it("returns access_token, expires_in, desktop_session_id, profile on valid verifier", async () => {
    const { handoffCode, verifier } = await seedHandoff();
    const res = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: verifier }), testEnv),
    );
    expect(res.status).toBe(200);
    const body = await res.json<{
      access_token: string;
      expires_in: number;
      desktop_session_id: string;
      profile: { display_name: string; avatar_url: string };
    }>();
    expect(body.access_token).toBeTruthy();
    expect(body.expires_in).toBe(3600);
    expect(body.desktop_session_id.length).toBeGreaterThan(40);
    expect(body.profile.display_name).toBe("Alice");
    // refresh_token must NOT be in the response body
    expect(JSON.stringify(body)).not.toContain("refresh-token-1");
  });

  it("writes SESSIONS with refresh_token + sub (refresh_token stays in Cloudflare)", async () => {
    const { handoffCode, verifier } = await seedHandoff();
    const res = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: verifier }), testEnv),
    );
    const body = await res.json<{ desktop_session_id: string }>();
    const session = await getSession(testEnv, body.desktop_session_id);
    expect(session).not.toBeNull();
    expect(session!.refresh_token).toBe("refresh-token-1");
    expect(session!.sub).toBe("u-1");
    expect(session!.created_at).toBeTruthy();
  });

  it("consumes handoff_code one time — second exchange returns 404", async () => {
    const { handoffCode, verifier } = await seedHandoff();
    const first = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: verifier }), testEnv),
    );
    expect(first.status).toBe(200);
    const second = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: verifier }), testEnv),
    );
    expect(second.status).toBe(404);
    const body = await second.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("HANDOFF_NOT_FOUND");
  });

  it("rejects wrong verifier with 400 and still consumes the code", async () => {
    const { handoffCode } = await seedHandoff();
    const wrongVerifier = randomToken(); // S256 won't match bound_challenge
    const res = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: wrongVerifier }), testEnv),
    );
    expect(res.status).toBe(400);
    const body = await res.json<{ error: { code: string } }>();
    expect(body.error.code).toBe("VERIFIER_MISMATCH");
    // one-time consume happened before verifier check → code now gone
    const retry = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: wrongVerifier }), testEnv),
    );
    expect(retry.status).toBe(404);
  });

  it("returns 404 for unknown handoff_code", async () => {
    const res = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: randomToken(), handoff_verifier: randomToken() }), testEnv),
    );
    expect(res.status).toBe(404);
  });

  it("returns 400 for missing fields", async () => {
    const res = await onRequestPost(makeContext(jsonPost(URL_EXCHANGE, {}), testEnv));
    expect(res.status).toBe(400);
  });

  it("falls back to expires_in=300 when handoff stored a non-positive value", async () => {
    const { handoffCode, verifier } = await seedHandoff({ expires_in: 0 });
    const res = await onRequestPost(
      makeContext(jsonPost(URL_EXCHANGE, { handoff_code: handoffCode, handoff_verifier: verifier }), testEnv),
    );
    const body = await res.json<{ expires_in: number }>();
    expect(body.expires_in).toBe(300);
  });
});
