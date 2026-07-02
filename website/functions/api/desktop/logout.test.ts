import { describe, it, expect, beforeEach } from "vitest";
import { onRequestPost } from "./logout";
import { makeContext, jsonPost, makeEnv } from "../../_lib/testutil";
import { putSession, getSession } from "../../_lib/kv";
import { randomToken } from "../../_lib/pkce";
import type { Env } from "../../_lib/env";

let testEnv: Env;
beforeEach(() => {
  testEnv = makeEnv(); // 每个用例新建内存 KV，保证隔离
});
const URL_LOGOUT = "https://test.example/api/desktop/logout";

describe("POST /api/desktop/logout", () => {
  it("deletes the session and returns 204", async () => {
    const id = randomToken(48);
    await putSession(testEnv, id, {
      refresh_token: "r",
      sub: "u",
      created_at: new Date().toISOString(),
    });
    const res = await onRequestPost(
      makeContext(jsonPost(URL_LOGOUT, { desktop_session_id: id }), testEnv),
    );
    expect(res.status).toBe(204);
    expect(await res.text()).toBe("");
    expect(await getSession(testEnv, id)).toBeNull();
  });

  it("is idempotent — 204 even for a non-existent session", async () => {
    const res = await onRequestPost(
      makeContext(jsonPost(URL_LOGOUT, { desktop_session_id: randomToken(48) }), testEnv),
    );
    expect(res.status).toBe(204);
  });

  it("returns 400 for missing desktop_session_id", async () => {
    const res = await onRequestPost(makeContext(jsonPost(URL_LOGOUT, {}), testEnv));
    expect(res.status).toBe(400);
  });
});
