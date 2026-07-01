#!/usr/bin/env python3
"""_ws_auth.py —— WS 首帧 auth 辅助（被 smoke-server.sh 在无 websocat 时调用）。

连接 WS_URL，发送 AUTH_FRAME（{"type":"auth",...}），打印收到的第一帧后退出。
不做断言（交给调用方 jq）；仅负责一次 send/recv。

环境变量:
  WS_URL      必填，如 ws://127.0.0.1:8080/ws
  AUTH_FRAME  必填，完整的 auth JSON 帧字符串
  WS_TIMEOUT  可选，单帧接收超时秒（默认 10）

依赖: pip install websockets （标准库无 WS 客户端）。
退出码: 0 成功打印一帧；1 连接/接收失败；3 缺 websockets 库。
"""
import os
import sys
import asyncio

WS_URL = os.environ.get("WS_URL", "")
AUTH_FRAME = os.environ.get("AUTH_FRAME", "")
TIMEOUT = float(os.environ.get("WS_TIMEOUT", "10"))


def _die(msg: str, code: int) -> None:
    print(msg, file=sys.stderr)
    sys.exit(code)


if not WS_URL or not AUTH_FRAME:
    _die("需要 WS_URL 与 AUTH_FRAME 环境变量", 1)

try:
    import websockets  # type: ignore
except ImportError:
    _die("缺少 websockets 库（pip install websockets）", 3)


async def main() -> int:
    try:
        async with websockets.connect(WS_URL, open_timeout=TIMEOUT, max_size=2 ** 20) as ws:
            await ws.send(AUTH_FRAME)
            frame = await asyncio.wait_for(ws.recv(), timeout=TIMEOUT)
            if isinstance(frame, bytes):
                frame = frame.decode("utf-8", "replace")
            print(frame)
            return 0
    except Exception as exc:  # noqa: BLE001 - 冒烟脚本，聚合报错即可
        print(f"WS 失败: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
