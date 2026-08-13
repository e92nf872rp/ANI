"""验证知识库图片的 OSS 链接能否通过 GET 下载/显示。

背景：文档解析时图片被内联为 markdown 链接（ani-kb-docs/{key}），但该
路径是相对 bucket 的私有对象 key，直接拼 MinIO 端点 GET 会因未签名返回 403。
本脚本验证正确方式：调 Core `POST /buckets/{id}/objects/presigned-url`
（method=GET）生成可下载链接，再用 HTTP GET 拉取，确认是否返回图片内容。

复用 kb-service 的 settings + CoreClient（tenant header 自动带上）。
"""
import asyncio
import json
import sys
from pathlib import Path

import httpx

# 复用 kb-service 的包（app.core.config / app.core_api.client）
KB_SERVICE = Path(__file__).resolve().parents[1] / "services" / "kb-service"
sys.path.insert(0, str(KB_SERVICE))

from app.core.config import settings  # noqa: E402
from app.core_api.client import CoreClient  # noqa: E402

TENANT = "00000000-0000-0000-0000-000000000001"
# 取自 p0_content_dedup_verified.txt 中记录的图片内联链接
IMAGE_KEY = (
    f"{TENANT}/2519f990-1ba4-4232-9a3e-b0085764b063/"
    "186f5783-40c7-4fda-9ba2-abd4fb95b13e/images/"
    "425b94f0db604b9681b59da0b63b1d92.png"
)


async def main() -> None:
    print("core_api_base_url:", settings.core_api_base_url)
    client = CoreClient(base_url=settings.core_api_base_url, tenant_id=TENANT)

    bucket_id = await client.get_bucket_id_by_name(name="kb-docs")
    print("bucket_id(kb-docs):", bucket_id)
    if not bucket_id:
        print("[FAIL] kb-docs 桶未找到")
        return

    # 1) 调 presigned-url 生成 GET 下载链接
    presigned = await client._client.post(
        f"/buckets/{bucket_id}/objects/presigned-url",
        json={"key": IMAGE_KEY, "method": "GET", "expires_hours": 1},
    )
    print("presigned status:", presigned.status_code)
    print("presigned body:", json.dumps(presigned.json())[:600])
    if presigned.status_code != 200:
        print("[FAIL] 生成 GET 链接失败")
        return

    dl = (presigned.json() or {}).get("download_url") or presigned.json().get("url")
    print("\ndownload_url:", (dl or "")[:180], "..." if dl and len(dl) > 180 else "")

    # 2) 用 GET 下载验证
    r = httpx.get(dl, timeout=30)
    print("\nGET status:", r.status_code)
    print("content-type:", r.headers.get("content-type"))
    print("body length:", len(r.content))
    if r.status_code == 200 and len(r.content) > 0:
        print("\n[PASS] 图片可通过 GET 下载/显示（签名链接有效）")
    else:
        print("\n[FAIL] 图片 GET 下载失败")


if __name__ == "__main__":
    asyncio.run(main())
