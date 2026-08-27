# VPC 列表接口链路 与 LocalNetworkService 命名解读

> 适用范围：网络域（VPC / Subnet / 安全组 / 负载均衡 / 路由）读取路径。
> 结论先行：**`GET /networks/vpcs` 返回的数据全部来自网关进程内存，当前没有任何环节访问数据库。**
> 数据库表已建、写库代码已备，但从未被装配使用（详见第 3 节）。

---

## 1. 请求链路：从一次 curl 到一块内存

以 `GET /api/v1/networks/vpcs` 为例，整条链路如下：

```
curl GET /api/v1/networks/vpcs
 │
 ▼
① 网关进程收到 HTTP 请求
   （services/ani-gateway/main.go 启动的常驻服务）
 │
 ▼
② 路由匹配："/networks/vpcs" (GET) → handler 函数 listVPCs
   （internal/router/network_resources.go:250 注册；
     router.go:95 完成路由组挂载）
 │
 ▼
③ handler 解析参数：租户 ID、名称前缀、状态过滤
   （network_resources.go:313 listVPCs）
 │
 ▼
④ handler 调用 service.ListVPCs(ctx, {tenant_id, name, state})
 │
 ▼
⑤ LocalNetworkService 遍历自身成员变量 vpcs（一个 Go map）：
    - 过滤非本租户的记录
    - 过滤 state=deleted 的记录（软删除）
    - 按名称前缀 / 状态二次过滤
    - 按 updated_at 倒序排序
   （pkg/adapters/runtime/network_service.go:220）
 │                ★ 全程没有一条 SQL，没有任何组件访问数据库
 ▼
⑥ 结果序列化为 JSON 返回
```

几个关键认知：

- **第⑤步就是数据终点。** 数据不是"存在某处再被读出来"，而是直接住在网关进程的一块内存里。它与普通程序变量没有本质区别——只是内容在运行时动态增删。
- **写入路径同样只进内存。** `CreateVPC` / `DeleteVPC` 操作的也是同一批 map；方法尾部虽然会调用 `upsertVPC` 尝试镜像到数据库，但由于 store 从未被注入，所有写库动作在入口处被 `if s.store == nil { return nil }` 短路（network_service.go:971 起）。
- **幂等键也在内存里**（`vpcIdempotency` 等 map），进程重启后重放创建请求会生成新资源，而不是返回原记录。

### 当前形态的实际影响

| 场景 | 结果 |
|---|---|
| 网关重启 / 滚动升级 | 所有 VPC 记录凭空消失 |
| 多副本部署 | 各副本各自维护一份数据，互不相通 |
| 底层 Kube-OVN 资源被人手动删除 | 内存中的状态不会变化，列表仍显示 available |
| 审计 / 对账 | 无历史可查 |

---

## 2. 为什么叫 LocalNetworkService

### Local 的真实含义："单机版"

类比游戏的"单机模式 vs 联机模式"：

- **单机模式（Local）**：数据放在自己内存里，不依赖外部存储，进程关闭即失；
- **联机模式（未来形态）**：数据存 Postgres，操作下发到真实的 Kube-OVN。

同一个接口背后可以插不同的实现，Go 接口不约束实现类的名字，所以先糊出来的这个版本需要一个称呼——作者选择了 `Local`。若叫作 `InMemoryNetworkService` 或 `StandaloneNetworkService`，含义会直白得多。

### 包内的命名潜规则

`pkg/adapters/runtime` 下有一套约定俗成的对照关系——**是否"出进程"访问外部系统，通过命名前缀区分**：

| 命名 | 含义 | 示例 |
|---|---|---|
| `Metadata*Store` | 出门访问 Postgres | `MetadataNetworkStore` |
| `KubeOVN*Adapter` | 出门访问 Kubernetes / Kube-OVN | `KubeOVNNetworkProviderAdapter` |
| `Local*` | **不出门，在本进程内自洽完成** | `LocalNetworkService` |

阅读口诀：看到 `NewLocalXxx`，脑子里自动替换为 **"Xxx 的内置默认实现"**。

### 这个名字留下的三个坑

1. **与网络领域词汇撞车。** 一个 VPC 服务叫 "Local"，第一直觉是"局域网 / VPC 内网"，而实际含义与此毫无关系。这是本次解读中最容易误判的点。
2. **并未严格兑现"不出门"。** 配置了 `NETWORK_PROVIDER=kubeovn_rest` 后，CreateVPC 会经渲染→dry-run→apply→observe 流水线真正操作 Kube-OVN（network_service.go:1011）。`Local` 描述的是"账本记在哪"，而非"操作边界在哪"。
3. **同族实现不完全守约。** 例如 `LocalInstanceService` 是接了数据库 store 干活的（instance_service.go:106）。因此 `Local` 只能可靠地理解为弱承诺："适配层提供的默认实现"，具体是否持久化必须回到装配点确认。

---

## 3. 背景：为什么读的是内存（三层真值现状）

理解上面两节后，一个自然的疑问是：迁移文件里明明有 `network_vpcs` 表，这是怎么回事？答案是三层真值各自完成了不同进度：

| 层 | 设计意图 | 当前进度 |
|---|---|---|
| 进程内存 map | 缓存 | ✅ 唯一实际持有数据的层，读写皆在此 |
| Postgres 表<br>`network_vpcs` 等 | 元数据持久层 | ⚠️ 半成品：表已建（migration `20260520000500_network_resources.sql`），写侧适配器 `MetadataNetworkStore` 已写完，但无任何进程装配它 |
| Kube-OVN | 数据面真值 | ⚠️ 仅在创建瞬间 observe 一次并回填内存；状态回读器（reconciler）已编写但无人周期性运行 |

补充两点背景：

- 这是**分期交付的欠账**，而非随机结果。开发记录中明确记载网络域当时处于 "Tier1 local profile"（development-records/sprint13-netroute-kubeovn-readiness.md）：先用零依赖的内存实现打通接口与 provider 管线，持久化排在后续切片——该切片尚未完成。
- `pkg/bootstrap/deps.go` 中其实存在一套完整正确的装配（deps.go:193 构建 store、:383 组装 reconciler），但该装配函数只有自身的单元测试在调用，属于**未接线的死代码**。网关在 `services/ani-gateway/network_runtime.go` 里另行手搓了一套不带 store 的装配。

### 后续修复方向（供参考）

按仓库"HANDLER 不绕过 PORT"的惯例，最小改动路径为三步：

1. 扩展 `ports.NetworkResourceStore`，增加 List / Get 读方法（当前它是纯写接口，network_resources.go:352）；
2. 在 `MetadataNetworkStore` 中用现成索引实现这两个查询；
3. 在网关装配点注入 `WithNetworkResourceStore(NewMetadataNetworkStore(metadataStore))`——`quotaMetadataStore` 已在 main.go 中就绪，可直接复用。

三项全部完成后，`ListVPCs` 签名里那个被丢弃的 `_ context.Context` 参数才会真正派上用场。

---

## 附：关键代码位置速查

| 内容 | 位置 |
|---|---|
| 路由注册 `/networks/vpcs` | `services/ani-gateway/internal/router/network_resources.go:250` |
| handler `listVPCs` | 同文件 `:313` |
| service 兜底逻辑（未注入时回退内存版） | 同文件 `newNetworkAPIWithService :239` |
| 网关侧 service 装配 | `services/ani-gateway/network_runtime.go:47` |
| 内存版服务本体 | `pkg/adapters/runtime/network_service.go`（ListVPCs `:220`，provider 管线 `:1011`） |
| 端口定义 NetworkService / NetworkResourceStore | `pkg/ports/network_resources.go:314 / :352` |
| 写库适配器（未被装配） | `pkg/adapters/runtime/network_store.go` |
| DB 表结构 | `deploy/migrations/20260520000500_network_resources.sql` |
| 未使用的完整装配（死代码） | `pkg/bootstrap/deps.go:193, :383` |
