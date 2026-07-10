# IMAGE-UPLOAD-PROXY-VNC-AUTH-A — Gateway 上传代理 + Console WS 鉴权放行

完成日期：2026-07-10

## 现场根因

1. **ISO 上传连不上**：`upload_url` 指向 `https://<node>:31001/v1beta1/upload`。CDI uploadproxy 证书 SAN 只有 `cdi-uploadproxy` / `cdi-uploadproxy.cdi.svc`，浏览器直连 NodePort IP 会 TLS 失败；CORS 也不完整。
2. **VNC 无法连接**：`POST /console` 返回的 `connect_url` 含短期 query token，但 Auth/RBAC 只把 `/instances/*/exec/*` 标为 public。浏览器 noVNC 无 JWT → `GET .../console/{session}?token=...` 返回 **401**。

## 修复

1. Gateway 新增 `POST|OPTIONS /api/v1/images/upload-proxy`：流式转发到集群内 `CDI_UPLOADPROXY_INTERNAL_URL`（默认 `https://cdi-uploadproxy.cdi.svc:443`），跳过上游自签校验；创建会话时把 `upload_url` 改写为 Gateway 同源代理地址。
2. Auth public path 增加 `/instances/*/console/*` 与 `/images/upload-proxy`（upload-proxy 凭 CDI upload token 鉴权）。
3. Hertz 开启 `WithStreamBody(true)` + 提高 `MaxRequestBodySize`，支持大 ISO。
4. isolated `business-stack` / `deploy.py` 注入 `CDI_UPLOADPROXY_INTERNAL_URL`。

## 验证

```bash
cd repo/services/ani-gateway
go test ./internal/middleware/ ./internal/router/ -run 'TestAuthPublic|TestAuthProtected|TestCreateImageUploadRewrites|TestProxyImageUpload|TestImageAPI' -count=1
```

## 边界

不标 production ready。生产环境仍应给 uploadproxy 配可信任证书或 Ingress；本批次是 isolated/dev 浏览器可达性修复。
