# KuberCloud ANI · 部署工程设计

> 版本 V1 | 广州常青云科技有限公司 | 内部产品规划文件

---

## 一、部署模式矩阵

| 部署目标 | K8s 来源 | 网络 | 存储 | 典型场景 |
|---|---|---|---|---|
| 裸物理服务器 | ANI Installer 全新安装 RKE2 | KubeOVN | Rook-Ceph / 本地 SSD | 客户自购服务器，无任何基础设施 |
| 虚拟机（KVM/VMware/Hyper-V）| ANI Installer 全新安装 RKE2 | KubeOVN | Rook-Ceph / vSAN | 客户有虚拟化平台，新建 VM 部署 |
| 云主机（阿里云/华为云/AWS ECS）| ANI Installer 全新安装 RKE2 | KubeOVN / CNI-Hybrid | 云盘 CSI + MinIO | 客户在公有云租用 ECS |
| 已有 K8s 集群 | 复用客户现有集群 | 保留现有 CNI | 保留现有 StorageClass | 客户已有 K8s 运维能力 |
| 公有云托管 K8s（EKS/ACK/CCE）| Karmada 纳管 | 云原生 CNI | 云原生 StorageClass | 混合云/多云管理 |

**关键规则：**
- 安装程序在每种模式下给出明确的前置条件检查，不满足则阻止安装并说明原因
- 已有 K8s 复用时，只部署 ANI 平台层组件，不触碰 K8s 自身配置

---

## 二、ANI Installer 设计（Go 单二进制）

**定位：** 类似 kubeadm/k0sctl，通过交互式向导完成全流程安装，是代理商工程师的核心工具。

**二进制名：** `ani-installer`，支持 x86_64 / ARM64，离线运行，无任何外网依赖。

### 2.1 主要命令

```bash
ani-installer preflight          # 仅做前置检查，输出报告，不安装任何东西
ani-installer new                # 全新安装向导（交互式 TUI）
ani-installer join               # 新增节点加入现有 ANI 集群
ani-installer attach-k8s         # 接入已有 K8s 集群（跳过 K8s 安装）
ani-installer upgrade            # 在线升级（应用 .anipatch 包）
ani-installer status             # 查看 ANI 平台各组件健康状态
ani-installer uninstall          # 卸载（--keep-data 可保留数据）
ani-installer add-san --ip <IP>  # 向现有证书追加 IP SAN
```

### 2.2 `ani-installer new` 向导流程（11 步）

```
Step 1   前置检查      OS类型、内核版本≥5.15、CPU架构、NTP时钟同步、端口冲突检测
Step 2   硬件检测      CPU品牌/核数/NUMA拓扑、GPU/NPU检测、内存总量、磁盘枚举、网卡枚举
Step 3   驱动安装      按检测结果自动安装所需驱动（NVIDIA/昇腾/海光），已安装则跳过
Step 4   部署模式      全新 K8s（RKE2）| 复用已有 K8s（attach-k8s 流程）
Step 5   网络配置      集群管理IP、Pod CIDR、Service CIDR、访问地址（域名/纯IP）
Step 6   证书配置      自动生成内部CA（推荐）| 导入已有证书（支持纯IP SAN）
Step 7   存储配置      本地裸盘路径（Rook-Ceph）| 已有 StorageClass
Step 8   组件选择      基础版 | 完整版，可选启用 Region/AZ 多集群模式
Step 9   安装摘要      展示所有配置，等待人工确认（✅ 确认 / ❌ 返回修改）
Step 10  执行安装      带进度条，每个子步骤完成显示 ✅ 或 ❌ + 错误原因
Step 11  安装完成      展示 Console/BOSS 访问地址、初始账号密码、CA 证书下载链接
```

**TUI 框架：** `bubbletea` + `lipgloss`（Go，Charm 出品，生产级 TUI 库）

### 2.3 前置检查详细项

| 检查项 | 最低要求 | 不满足时 |
|---|---|---|
| OS 类型 | Ubuntu 22.04 / UOS 20 / 麒麟 V10 | 警告（不阻止，但不保证） |
| 内核版本 | ≥ 5.15 | 阻止安装 |
| CPU 架构 | x86_64 或 ARM64 | 阻止安装 |
| 内存 | 管理节点 ≥ 16GB，工作节点 ≥ 32GB | 警告 |
| 系统盘 | ≥ 100GB 可用 | 阻止安装 |
| 数据盘（裸盘）| ≥ 1 块未挂载裸盘（用于 Rook-Ceph）| 警告（无盘则无分布式存储） |
| NTP 时钟 | 节点间时差 ≤ 5 秒 | 阻止安装（K8s 证书对时间敏感）|
| 端口冲突 | 6443/2379/2380/10250 未占用 | 阻止安装 |
| SELinux/AppArmor | 建议 permissive | 警告 |

---

## 三、硬件自动检测与驱动管理（Go）

### 3.1 检测逻辑

**CPU 检测（读取 `/proc/cpuinfo` + `lscpu`）：**
- 品牌识别：Intel / AMD / 鲲鹏（Kunpeng）/ 飞腾（Phytium）/ 海光（Hygon）/ 龙芯（Loongson）
- 架构确认：x86_64 / ARM64
- NUMA 拓扑抓取（影响 GPU 调度 NUMA 亲和性配置）

**GPU/NPU 检测（按优先级顺序尝试）：**
```
1. nvidia-smi → NVIDIA GPU（获取型号/显存/驱动版本/CUDA版本）
2. npu-smi   → 华为昇腾 NPU（获取型号/固件版本）
3. hy-smi    → 海光 DCU（获取型号）
4. rocm-smi  → AMD GPU（获取型号）
5. 无结果    → 纯 CPU 模式（知识库问答可用，推理限速）
```

**存储检测（`lsblk` + `/proc/mounts`）：**
- 枚举所有未挂载裸盘，按大小排序供用户选择
- 磁盘类型：NVMe / SATA SSD / HDD（给出性能预期说明）
- 最小推荐：系统盘 ≥ 100GB，数据盘 ≥ 500GB

**网络检测：**
- 枚举活跃网卡（排除 lo/virbr），按速率排序推荐管理网卡
- 检测 InfiniBand/RoCE 网卡（存在时提示可启用 GPU RDMA 高速互联）
- MTU 检测（< 1500 时警告 Jumbo Frame 配置建议）

### 3.2 驱动安装策略

**所有驱动包离线随 ANI 安装包分发，安装时不接触外网。**

| GPU 品牌 | 安装步骤 | 随包组件 |
|---|---|---|
| NVIDIA | 离线 rpm/deb 包 → CUDA Driver → NVIDIA Container Toolkit → 验证 nvidia-smi | 已打包 |
| 华为昇腾 | 离线 Ascend-CANN 包 → NPU 驱动 → CANN 工具包 → 验证 npu-smi | 已打包 |
| 海光 DCU | 离线 DCU 驱动包 → 验证 hy-smi | 已打包 |
| 无 GPU | 跳过，纯 CPU 模式启动 | — |

**驱动版本映射表**（内置于 installer 二进制，可通过 patch 更新）：
```yaml
nvidia:
  - gpu_model: "A100"
    driver_version: "535.104.05"
    cuda_version: "12.2"
  - gpu_model: "H100"
    driver_version: "535.104.05"
    cuda_version: "12.2"
  - gpu_model: "A10"
    driver_version: "525.85.12"
    cuda_version: "12.0"
ascend:
  - model: "910B"
    cann_version: "7.0.0"
  - model: "910C"
    cann_version: "8.0.0"
```

**K8s 层驱动生命周期（安装完成后由 K8s Operator 接管）：**
- NVIDIA：`nvidia-gpu-operator`（DaemonSet，自动化驱动更新和版本管理）
- 昇腾：`ascend-device-plugin`
- 海光 DCU：`hygon-dcu-device-plugin`

---

## 四、复用已有 K8s 集群（`ani-installer attach-k8s`）

### 4.1 自动兼容性检查

```
检查项                    通过条件              失败时行为
─────────────────────────────────────────────────────────
K8s 版本                  ≥ 1.28               阻止，提示升级
CNI 类型                  KubeOVN → 直接复用    其他 CNI → 警告功能受限，用户确认
                          Calico/Flannel →      无 VPC 多租户，继续/退出由用户选
StorageClass 存在          ≥ 1 个 SC            提示选择，无则安装 local-path-provisioner
IngressClass 存在          检测到则询问复用       否则安装 Nginx Ingress
```

### 4.2 在已有 K8s 上部署 ANI

安装程序只执行以下操作，不触碰 K8s 自身：

```
1. 创建命名空间：ani-system
2. 安装 ANI CRD 集合（kubectl apply -f ani-crds.yaml）
3. Helm 安装 ANI 平台组件：
   - ani-gateway
   - ani-model-service
   - ani-kb-service
   - ani-auth-service
   - ani-boss
4. 按用户选择配置后端存储：
   - PostgreSQL：新建 PVC | 指向外部 PG 实例（连接串）
   - MinIO：新建部署 | 指向外部 S3 兼容存储（Endpoint + Key）
5. 生成内部 CA 证书（或导入用户提供的证书）
6. 输出访问地址和初始凭证
```

---

## 五、多 Region/AZ 架构（Karmada）

### 5.1 选型理由

- K8s Federation v1/v2 已废弃，不再维护
- **Karmada** 是 CNCF 孵化项目，华为主导开发，与信创路线一致
- 能力覆盖：跨集群资源分发、聚合 API、故障转移、公有云 K8s 纳管

### 5.2 整体架构

```
┌──────────────────────────────────────────────────────────────┐
│  ANI 管理平面（Karmada Control Plane）                        │
│  ┌──────────────┐ ┌──────────────┐ ┌────────────────────┐   │
│  │ Karmada API  │ │ ANI Gateway  │ │ BOSS 后台           │   │
│  │ Server       │ │（全局入口）   │ │（运营运维）         │   │
│  └──────────────┘ └──────────────┘ └────────────────────┘   │
│  全局 PostgreSQL（主从，跨 AZ）  · 全局 Prometheus + Grafana  │
└────────────────────┬──────────────────┬──────────────────────┘
                     │                  │
         ┌───────────▼────┐  ┌──────────▼──────────────┐
         │  Region：华东   │  │  Region：公有云           │
         │  ┌─AZ-1a─┐     │  │  阿里云 ACK / 华为 CCE   │
         │  │K8s集群 │     │  │  通过 Karmada API 纳管   │
         │  └────────┘     │  └─────────────────────────┘
         │  ┌─AZ-1b─┐     │
         │  │K8s集群 │     │
         │  └────────┘     │
         └────────────────┘
```

### 5.3 Region/AZ 资源抽象（ANI 扩展 CRD）

```yaml
# Region 定义
apiVersion: ani.kubercloud.io/v1
kind: ANIRegion
metadata:
  name: cn-east
spec:
  displayName: 华东区域
  availabilityZones:
    - cn-east-1a
    - cn-east-1b

---
# AZ 定义（对应一个 Karmada 成员集群）
apiVersion: ani.kubercloud.io/v1
kind: ANIAvailabilityZone
metadata:
  name: cn-east-1a
spec:
  region: cn-east
  karmadaCluster: cluster-east-1a
  gpuCapacity:
    nvidia-a100: 8
    ascend-910b: 4
  status:
    available: true
    gpuUsed:
      nvidia-a100: 3
```

### 5.4 租户 AZ 放置（效仿公有云 UX）

租户创建推理服务、知识库等资源时可指定：

```json
{
  "model": "qwen2.5-72b",
  "placement": {
    "region": "cn-east",
    "az": "cn-east-1a"    // 不填则由调度器在 region 内自动均衡
  }
}
```

Karmada `PropagationPolicy` 自动将 workload 分发到对应成员集群。

### 5.5 跨 AZ 资源能力

| 资源类型 | 单/跨 AZ | 实现方案 |
|---|---|---|
| VPC / 子网 | 单 AZ | KubeOVN VPC，跨 AZ 走 Peering |
| LoadBalancer | 跨 AZ 全局 VIP | MetalLB BGP 模式 + Anycast |
| NAT Gateway | 单 AZ | KubeOVN EIP + SNAT |
| 推理服务 | 跨 AZ 多副本 | Karmada PropagationPolicy |
| 知识库 | 主 AZ，跨 AZ 只读副本 | PostgreSQL 流复制 |
| 对象存储（模型）| 跨 AZ 复制 | MinIO Site Replication |

**Phase 1 交付范围：** Karmada 纳管 + Region/AZ CRD + 租户 AZ 放置 API
**Phase 2 交付范围：** 跨 AZ LB（全局 VIP）、跨 AZ NAT、MinIO Site Replication

---

## 六、多租户计量系统（Phase 1）

### 6.1 计量维度

```
GPU-Hours       = GPU 卡数量 × 使用时长（分钟粒度）
CPU-Hours       = CPU 核数 × 使用时长
Memory-GBHours  = 内存 GB × 使用时长
Storage-GBDays  = 存储 GB × 天数
Input-Tokens    = LLM 推理输入 Token 数
Output-Tokens   = LLM 推理输出 Token 数
KB-Queries      = 知识库问答调用次数
```

### 6.2 采集架构

```
DCGM Exporter ──→ Prometheus ──→ 计量采集器（Go）──→ TimescaleDB
K8s Metrics   ──→ Prometheus ──→       ↑                   │
ANI Gateway 审计日志 ──────────────────┘                   │
MinIO 存储事件 ────────────────────────────────────────────┤
                                                           ↓
                                              BOSS 用量报表 + 告警
```

**存储：** TimescaleDB（PostgreSQL 时序扩展，无需新技术栈）

**数据表：** `metering_records (tenant_id, resource_type, az, quantity, unit, recorded_at)`

### 6.3 计费预留（Phase 2）

Phase 1 只采集和存储用量数据，不做计费结算。Phase 2 在此基础上追加：
- 价格目录服务
- 账单生成引擎
- 费用预警通知
- 报表导出（CSV / PDF）

---

## 七、在线 Patch 升级系统

版本号、升级兼容性和发布门禁以 `ANI-12-版本管理策略.md` 为准。

### 7.1 Patch 包格式（`.anipatch`）

```
patch-v1.1.0.anipatch（gzip 压缩 tar 归档）
├── manifest.yaml           # 元信息：版本号、兼容的 from_versions、组件清单
├── images/                 # 离线容器镜像（OCI tar 格式）
│   ├── ani-gateway-v1.1.0.tar
│   └── vllm-v0.7.0.tar
├── charts/                 # Helm Chart 更新包
│   └── ani-platform-v1.1.0.tgz
├── migrations/             # 数据库迁移（按序号顺序执行）
│   ├── 001_add_region_table.sql
│   └── 002_add_metering_index.sql
├── scripts/                # 前置/后置逻辑（Go 编译为二进制，避免 shell 依赖）
│   ├── pre-upgrade
│   └── post-upgrade
└── signature.sig           # 平台私钥签名（RSA-PSS），安装前强制验签
```

`manifest.yaml` 必须包含：

```yaml
version: v1.1.0
from_versions:
  - v1.0.x
min_supported_version: v1.0.0
rollback_supported: true
components:
  - name: ani-gateway
    version: v1.1.0
  - name: task-service
    version: v1.1.0
migrations:
  - 20260601_002_add_regions.sql
```

不兼容升级必须按 `ANI-12` 判定是否需要 MAJOR 版本，不能伪装成 patch 包。

### 7.2 升级 Operator（Go，K8s Operator）

```yaml
apiVersion: ani.kubercloud.io/v1
kind: ANIPatch
metadata:
  name: upgrade-v1.1.0
spec:
  version: "1.1.0"
  source: uploaded              # 已上传到平台存储
  dryRun: false
  maintenanceWindow: "02:00"    # 可选，仅在该时间点后执行
```

**Controller 执行流程：**
```
1. 验签 signature.sig（平台公钥内置于 operator 镜像）
2. 检查兼容性（当前版本是否在 from_versions 列表中）
3. dryRun 模式 → 仅输出变更计划，不执行
4. 执行 pre-upgrade 二进制
5. 逐组件 Helm Rolling Upgrade（--atomic，失败自动回滚该组件）
6. 执行数据库迁移脚本（Atlas 迁移工具）
7. 执行 post-upgrade 二进制
8. 全组件健康检查（就绪探针 + 自定义健康端点）
9. 失败时触发全量回滚并 Alert
```

### 7.3 BOSS 升级管理界面

- 当前版本面板（各组件版本号、上次升级时间）
- Patch 包上传（拖拽，显示上传进度）
- Dry-Run 检查结果展示（变更组件清单、DB 迁移预览）
- 一键升级（二次确认弹窗）
- 升级进度实时追踪（SSE 流式，逐组件状态）
- 升级历史记录（版本、时间、操作人、结果）

---

## 八、BOSS 白牌化

### 8.1 可配置项

| 配置 Key | 类型 | 说明 |
|---|---|---|
| `platform_name` | string | 产品名称（替换"KuberCloud ANI"）|
| `logo_light` | 文件（PNG/SVG ≤ 2MB）| 亮色背景 Logo |
| `logo_dark` | 文件（PNG/SVG ≤ 2MB）| 深色背景 Logo |
| `favicon` | 文件（ICO/PNG 32×32）| 浏览器标签图标 |
| `primary_color` | HEX 字符串 | 主色调（按钮、链接、高亮）|
| `secondary_color` | HEX 字符串 | 辅助色 |
| `login_bg_image` | 文件（可选）| 登录页背景图 |
| `icp_number` | string | 页面底部 ICP 备案号（合规要求）|

### 8.2 实现机制

- 品牌配置存入 PostgreSQL `platform_branding` 表
- 图片文件存入 MinIO `ani-branding` Bucket，通过签名 URL 访问
- API：`GET /api/v1/branding`（无需认证，首屏加载必需）
- 前端通过 CSS 自定义属性（`--color-primary` 等）动态应用，**无需重新构建**

---

## 九、无公网域名 / 纯 IP 部署

### 9.1 内部 CA 方案

安装时自动生成内部根 CA（默认选项，无需用户操作）：

```
ANI Root CA（自签名，有效期 10 年）
  └── ANI Intermediate CA（有效期 5 年）
        ├── ani-gateway（IP SAN: 管理IP, 每个节点IP）
        ├── harbor（IP SAN: Harbor 节点IP）
        ├── dex（IP SAN: Dex 节点IP）  ← 关键：OIDC issuer 必须 HTTPS
        └── *.ani-system.svc.cluster.local（内部服务通配符）
```

证书有效期：叶证书 90 天，cert-manager 自动轮换，零人工干预。

### 9.2 组件无域名适配清单

| 组件 | 适配方式 | 注意事项 |
|---|---|---|
| cert-manager | `selfSigned` / `ca` Issuer，`ipAddresses` 字段 | ✅ 原生支持 IP SAN |
| ANI Gateway | IP SAN 证书，Hertz TLS 配置 | ✅ |
| Harbor | `hostname` 配置为 IP，使用内部 CA 证书 | ✅ |
| Dex | `issuer: https://<IP>:<port>`，IP SAN 证书 | ⚠️ OIDC 规范强制 HTTPS |
| Grafana | 集群内 DNS 访问，无需外部证书 | ✅ |
| vLLM | 内部 HTTP，不对外暴露 | ✅ |
| Milvus | 内部 gRPC，不对外暴露 | ✅ |

**关键约束：** Dex 的 `issuer` URL 必须 HTTPS（OIDC 规范要求），所以即使只有 IP，也必须为 Dex 配置 IP SAN 证书。`ani-installer new` 的 Step 6 会自动处理。

### 9.3 CA 证书分发指引（安装完成后第一步）

BOSS 首次登录后引导页提示：

```
BOSS → 设置 → 平台安全 → 根CA证书

[下载 ani-root-ca.crt]

分发方式：
  Windows（GPO）: 计算机配置 → 证书 → 受信任的根证书颁发机构
  Linux:          cp ani-root-ca.crt /usr/local/share/ca-certificates/
                  update-ca-certificates
  macOS:          security add-trusted-cert -d -r trustRoot ani-root-ca.crt
```

安装 CA 后，用户通过 `https://<IP>` 访问 Console，浏览器显示安全锁，无警告。

---

## 十、代理商工程师培训与交付体系

### 10.1 培训目标（2 天完成）

培训后工程师应能独立完成：
- 从裸机/VM 到平台可用的全流程安装（含 GPU 驱动安装）
- 常见部署失败场景处理（网络不通、驱动失败、磁盘不足）
- 平台基础功能验收测试
- 指导客户 IT 完成 CA 证书信任配置

### 10.2 随平台交付的文档清单

| 文档 | 格式 | 用途 |
|---|---|---|
| 《快速部署手册》 | PDF，A4 两页 | 裸机→可用 10 步流程图，现场速查 |
| 《完整部署指南》 | PDF | 所有参数详细说明、故障排查树 |
| 《现场勘察前置清单》 | Excel | 客户环境确认表（硬件/网络/OS/磁盘）|
| 《验收测试手册》 | PDF | 功能验收标准操作步骤（带截图）|
| 《部署演示视频》 | MP4，可离线 | 完整部署流程录屏 |
| 《常见问题 FAQ》 | PDF | Top-20 故障及解决方法 |
| 《运维手册》 | PDF | 日常运维、备份恢复、升级操作 |

### 10.3 标准交付 SOP

```
T-3天  工程师与客户 IT 确认《前置清单》（OS 版本、IP 规划、磁盘准备）
T-1天  工程师到达客户现场，通过 U 盘 / 局域网分发 ANI 离线安装包
T+0 上午  执行 ani-installer preflight，确认环境就绪，修复问题
T+0 下午  执行 ani-installer new，全程约 1.5~2 小时
T+1 上午  执行《验收测试手册》，客户 IT 完成 CA 证书信任配置
T+1 下午  签署《验收确认单》，移交运维文档，培训客户 IT 运维操作
```

---

## 十一、工作量评估与时间线

| 模块 | 语言 | 工期 | 纳入月份 |
|---|---|---|---|
| `ani-installer` 主体（TUI + 向导 + RKE2 安装）| Go | 3 周 | M1 |
| 硬件检测 + 驱动安装模块 | Go | 2 周 | M1 |
| 内部 CA + cert-manager 配置 + 证书 API | Go | 1 周 | M1 |
| `attach-k8s` 复用已有 K8s | Go | 1 周 | M2 |
| Karmada 纳管 + Region/AZ CRD + Controller | Go | 3 周 | M2-M3 |
| 计量服务（TimescaleDB + 采集 Pipeline）| Go | 2 周 | M3-M4 |
| Patch 升级 Operator + BOSS 升级界面 | Go + TS | 2 周 | M4 |
| BOSS 白牌化（API + 前端动态加载）| Go + TS | 1 周 | M4 |
| 跨 AZ LB / NAT / MinIO 复制 | Go | 2 周 | Phase 2 |
| 计费结算引擎 + 账单生成 | Go | 2 周 | Phase 2 |
| 离线安装包制作工具链 | Go/Shell | 1 周 | M5 |
| 培训材料（文档 + 视频）| 产品/运营 | 2 周 | M5 |

**Phase 1（9月30日）交付范围：**
- ✅ ani-installer 全流程（裸机 / VM / 已有 K8s）
- ✅ 硬件检测 + NVIDIA / 昇腾 / 海光驱动安装
- ✅ 内部 CA + 纯 IP HTTPS 访问
- ✅ Karmada 基础版（Region/AZ 定义 + 租户放置 API）
- ✅ 计量基础版（用量记录，无账单）
- ✅ Patch 升级机制
- ✅ BOSS 白牌化

---

## 十二、验收测试要点

| 测试场景 | 验证方法 | 通过标准 |
|---|---|---|
| 裸机全新安装 | 全新 Ubuntu 22.04 + A100 机器执行 `ani-installer new` | ≤ 2 小时完成，无报错 |
| 复用已有 K8s | 在 k3s 集群执行 `ani-installer attach-k8s` | ANI 平台正常启动 |
| 纯 IP 访问 | 只配置 IP（无域名），浏览器访问 `https://<IP>` | 无证书警告，正常登录 |
| GPU 驱动安装 | 安装前无驱动，安装后执行 `nvidia-smi` | 识别 GPU，驱动版本正确 |
| Patch 升级 | 制作最小测试 patch（仅改版本号），执行升级 + 验证回滚 | 升级成功 / 失败自动回滚 |
| 白牌化 | 上传新 Logo + 修改主色，刷新 Console | 即时生效，无需重启 |
| 计量 | 调用推理 10 次，查询计量 DB | Token 数记录准确 |
| AZ 放置 | 创建推理服务时指定 AZ，验证 Pod 落在对应集群 | Karmada 分发正确 |
