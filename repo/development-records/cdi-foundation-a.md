# CDI-FOUNDATION-A — CDI v1.65.0 foundation deployment

完成日期：2026-07-09

## 范围

为 ISO/CDI image upload 后续批次补齐 isolated cluster foundation 的 CDI 安装入口：固定 CDI `v1.65.0`，沿用 KubeVirt foundation 安装风格，把官方 release operator manifest 纳入 `deploy/isolated/cluster-foundation/yaml/`，并把 manifest 中的 CDI 组件镜像指向 `docker.changqingyun.cn/mirror/kubevirt/*:v1.65.0`。

> 边界声明：本批次只交付 ANI Core deployment foundation。它不实现 Images API、DataVolume upload adapter、VM ISO boot renderer，不修改 Services / `frontends/`，也不声明 production ready。

## 实现内容

- 新增 CDI operator / CR / uploadproxy NodePort 清单，`cdi-uploadproxy-nodeport` 固定 NodePort `31001`。
- `deploy.py` 增加 CDI v1.65.0 mirror 列表：operator、controller、apiserver、uploadproxy、importer、uploadserver、cloner。
- `deploy_foundation()` 顺序调整为 KubeVirt → CDI → Rook → Ceph StorageClass → Harbor。
- `install_cdi()` 执行 apply operator、等待 `cdis.cdi.kubevirt.io` / `datavolumes.cdi.kubevirt.io` CRD、apply CDI CR、等待 CDI Available、apply uploadproxy NodePort。

## 验证

- `python3 deploy/isolated/deploy.py render`：通过，重新生成 `cluster-foundation-all.yaml` 并包含 CDI operator / CR / NodePort 清单。
- `python3 scripts/validate_yaml.py deploy/isolated/cluster-foundation/yaml/06-cdi-operator.yaml deploy/isolated/cluster-foundation/yaml/07-cdi-cr.yaml deploy/isolated/cluster-foundation/yaml/08-cdi-uploadproxy-nodeport.yaml deploy/isolated/cluster-foundation/yaml/cluster-foundation-all.yaml`：`validated 4 YAML files`。
- `python3 scripts/validate_doc_entrypoints.py`：`document entrypoint boundaries valid`。
- `make validate-architecture`：通过（本环境无 `python` 命令，使用临时 PATH shim 指向 `python3` 后执行）。
- `make test`：通过（本环境无 `python` 命令，使用同一临时 PATH shim 后执行）。
- `git diff --check`：通过。
- Live install：kubectl context 可达；执行 `install_cdi()` 只安装/升级 CDI 部分，未重新部署 Rook/Ceph。结果：`CDI cdi phase=Deployed`，`cdi-apiserver` / `cdi-deployment` / `cdi-operator` / `cdi-uploadproxy` 均 `1/1 Running`，`cdi-uploadproxy-nodeport` 为 `NodePort 443:31001/TCP`，CDI CRD（含 `datavolumes.cdi.kubevirt.io`）已建立。

## 注意事项

- CDI v1.65.0 的 `CDI` CRD 使用 strict schema，拒绝 `spec.imageRegistry` / `spec.imageTag` 字段；本批次改为在 operator manifest 的 `CONTROLLER_IMAGE` / `IMPORTER_IMAGE` / `APISERVER_IMAGE` / `UPLOAD_*` env 与 operator deployment image 中固定 mirror 镜像。
- operator 管理的同名 `cdi-uploadproxy` Service 会自动回写为 `ClusterIP`；为提供稳定 NodePort，本批次新增未被 operator 管理的 `cdi-uploadproxy-nodeport` Service，使用同一 selector 暴露 `31001`。

## 关键文件

- `deploy/isolated/deploy.py`
- `deploy/isolated/cluster-foundation/yaml/06-cdi-operator.yaml`
- `deploy/isolated/cluster-foundation/yaml/07-cdi-cr.yaml`
- `deploy/isolated/cluster-foundation/yaml/08-cdi-uploadproxy-nodeport.yaml`
- `deploy/isolated/cluster-foundation/README.md`
