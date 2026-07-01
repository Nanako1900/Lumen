#!/usr/bin/env python3
"""gen-test-jwt.py —— 无真实 IdP 时，生成 RS256 测试 JWT + 匹配的 JWKS。

用于**本地**校验 Go 服务端的 JWKS 验签路径（不接真实 IdP）：
  1) 本脚本生成一对 RSA 私钥/公钥，签一个 RS256 的 access_token（可控 iss/aud/sub/exp/name/picture）；
  2) 同时输出一个 JWKS（含该公钥），写到文件（默认 ./.local/jwks.json）；
  3) 你把服务端的 LUMEN_OAUTH_JWKS_URL 指向这个 JWKS（例如用任意静态服务器托管该文件，
     或指向 file:// 若服务端支持；否则 `python3 -m http.server` 托管 ./.local），
     LUMEN_OAUTH_ISSUER / LUMEN_OAUTH_AUDIENCE 设为与本脚本一致的值；
  4) 用打印出的 ACCESS_TOKEN 跑 scripts/smoke-server.sh。

⚠ 仅供本地/测试。生成的私钥留在本地 .local/（已被 .gitignore 的 *.pem/.local 覆盖）。
   切勿把测试 JWKS/issuer 用于生产（生产用真实 IdP 的 JWKS）。

用法:
  python3 scripts/gen-test-jwt.py \
    --iss https://auth.example.com/realms/lumen \
    --aud lumen-api \
    --sub sub-abc \
    --name "Test User" \
    [--ttl 3600] [--out-dir ./.local] [--kid test-key-1]

输出:
  - 打印 ACCESS_TOKEN=<jwt>（可 `export` 后给 smoke-server.sh）
  - 写 <out-dir>/jwks.json（服务端 LUMEN_OAUTH_JWKS_URL 指向它）
  - 写 <out-dir>/private.pem（本地私钥，勿提交）

依赖: pip install PyJWT cryptography
"""
import argparse
import base64
import json
import os
import sys
import time


def b64url_uint(n: int) -> str:
    raw = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def main() -> int:
    ap = argparse.ArgumentParser(description="生成 RS256 测试 JWT + JWKS（本地校验服务端验签）")
    ap.add_argument("--iss", required=True, help="issuer（须与服务端 LUMEN_OAUTH_ISSUER 一致）")
    ap.add_argument("--aud", default="lumen-api", help="audience（须含服务端 LUMEN_OAUTH_AUDIENCE，默认 lumen-api）")
    ap.add_argument("--sub", default="sub-test", help="subject（可放进 LUMEN_OWNER_SUBJECTS 以测 owner）")
    ap.add_argument("--name", default="Test User", help="name claim（-> display_name）")
    ap.add_argument("--picture", default="", help="picture claim（-> avatar_url，可空）")
    ap.add_argument("--ttl", type=int, default=3600, help="有效期秒（默认 3600）")
    ap.add_argument("--kid", default="test-key-1", help="key id")
    ap.add_argument("--out-dir", default="./.local", help="输出目录（默认 ./.local，已被 gitignore）")
    args = ap.parse_args()

    try:
        import jwt  # PyJWT
        from cryptography.hazmat.primitives.asymmetric import rsa
        from cryptography.hazmat.primitives import serialization
    except ImportError:
        print("缺少依赖：pip install PyJWT cryptography", file=sys.stderr)
        return 3

    os.makedirs(args.out_dir, exist_ok=True)

    # 生成 RSA 私钥
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    priv_pem = key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )
    priv_path = os.path.join(args.out_dir, "private.pem")
    with open(priv_path, "wb") as f:
        f.write(priv_pem)

    # 组 JWKS（公钥）
    pub_numbers = key.public_key().public_numbers()
    jwk = {
        "kty": "RSA",
        "use": "sig",
        "alg": "RS256",
        "kid": args.kid,
        "n": b64url_uint(pub_numbers.n),
        "e": b64url_uint(pub_numbers.e),
    }
    jwks = {"keys": [jwk]}
    jwks_path = os.path.join(args.out_dir, "jwks.json")
    with open(jwks_path, "w", encoding="utf-8") as f:
        json.dump(jwks, f, indent=2)

    # 签 JWT
    now = int(time.time())
    claims = {
        "iss": args.iss,
        "aud": args.aud,
        "sub": args.sub,
        "iat": now,
        "nbf": now,
        "exp": now + args.ttl,
        "name": args.name,
    }
    if args.picture:
        claims["picture"] = args.picture
    token = jwt.encode(claims, priv_pem, algorithm="RS256", headers={"kid": args.kid})

    # 输出（token 到 stdout 便于 eval/export；其余到 stderr）
    print(f"# JWKS 已写入: {jwks_path}", file=sys.stderr)
    print(f"# 私钥已写入: {priv_path}（勿提交）", file=sys.stderr)
    print("# 让服务端指向该 JWKS，例如：", file=sys.stderr)
    print(f"#   ( cd {args.out_dir} && python3 -m http.server 9999 )  # 托管 jwks.json", file=sys.stderr)
    print(f"#   export LUMEN_OAUTH_JWKS_URL=http://127.0.0.1:9999/jwks.json", file=sys.stderr)
    print(f"#   export LUMEN_OAUTH_ISSUER='{args.iss}'", file=sys.stderr)
    print(f"#   export LUMEN_OAUTH_AUDIENCE='{args.aud}'", file=sys.stderr)
    print("# 然后：", file=sys.stderr)
    print(f"#   export ACCESS_TOKEN='<下面这行>' && bash scripts/smoke-server.sh", file=sys.stderr)
    print("", file=sys.stderr)
    # stdout：仅 token 本身（方便 `ACCESS_TOKEN=$(python3 scripts/gen-test-jwt.py ...)`）
    print(token)
    return 0


if __name__ == "__main__":
    sys.exit(main())
