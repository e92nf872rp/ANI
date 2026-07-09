# ISO-CDI-LIVE-HARDENING-A — Live 冒烟修复与 isolated 可复跑收口

完成日期：2026-07-09

## 范围

本批次收口 ISO/CDI 上传与 `boot_media=iso` 在真实 isolated 集群冒烟中暴露的问题，并去掉应用层写死的 `ani-rbd-ssd` 默认值。

> 边界声明：只改 Core adapter / isolated deploy / OpenAPI 描述与文档。不改 Services / `frontends/`。不标 production ready。`DELETE /images` 占用检查与 list 分页仍未实现。

## 现场暴露的问题与修复

1. **Gateway CDI RBAC 缺失**：`GET/POST /images*` 对 DataVolume / UploadTokenRequest 返回 403。  
   修复：`business-stack.yaml` ClusterRole 增加 `cdi.kubevirt.io/datavolumes` 与 `upload.cdi.kubevirt.io/uploadtokenrequests`。
2. **Block 卷 Permission denied**：`ani-rbd-ssd` StorageProfile 偏好 Block+RWX 时，CDI uploadserver/importer 无法打开 `/dev/cdi-block-volume`。  
   修复：ISO 上传 DV 与空白系统盘 `dataVolumeTemplates` 强制 `Filesystem` + `ReadWriteOnce`；isolated 增加 `09-cdi-storageprofile-filesystem.yaml`，foundation 在 SC 就绪后 apply。
3. **应用层写死 StorageClass**：未传 `storage_class` 时硬编码 `ani-rbd-ssd`。  
   修复：未传则省略 `storageClassName`，走集群 default StorageClass（`deploy.py` 已将 `ani-rbd-ssd` 标为 default）。显式传入仍可覆盖。
4. **isolated 节点 IP 写死**：`CDI_UPLOADPROXY_URL` / console base URL 曾写死实验室 IP。  
   修复：yaml 占位 `127.0.0.1`；`deploy_business` 用 `node_ip()` 注入 `CDI_UPLOADPROXY_URL`、`IMAGE_IMPORT_PROVIDER=cdi_rest` 与既有 console URL。

## 验证

```bash
cd /root/kubercon/ANI/repo/pkg && go test ./adapters/runtime/ -run 'TestCDIImageImport|TestRenderVMWithISO' -count=1
cd /root/kubercon/ANI/repo/services/ani-gateway && go test ./internal/router/ -run TestDemoInstanceServiceCreatesVMWithISOBootMedia -count=1
cd /root/kubercon/ANI/repo && make validate-architecture && git diff --check
```

## 相关文件

- `repo/pkg/adapters/runtime/cdi_image_import.go`
- `repo/pkg/adapters/runtime/dryrun_renderer.go`
- `repo/deploy/isolated/business-stack.yaml`
- `repo/deploy/isolated/deploy.py`
- `repo/deploy/isolated/cluster-foundation/yaml/09-cdi-storageprofile-filesystem.yaml`
- `repo/deploy/isolated/README.md`
- `repo/api/openapi/v1.yaml`
- `repo/development-records/frontend-prompt-images-iso-upload.md`
