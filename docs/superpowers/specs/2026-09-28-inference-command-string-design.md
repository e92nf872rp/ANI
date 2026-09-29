# 推理服务自定义命令文本输入

用户已选择普通命令行字符串输入。前端文本框直接提交
`engine.command: "python3 -m vllm.entrypoints.openai.api_server --model /models/qwen --dtype auto"`。

## 方案与边界

推荐仅扩展 HTTP 创建入参：接受文本或原有 argv 数组。替代方案是只接收文本（会破坏旧客户端），或隐式 `sh -c`（会改变执行语义且阻断现有模型路径替换）；均不采用。

Services v1 新增创建专用的 `CreateInferenceServiceEngine` schema，`command` 使用 string/array 的 `oneOf`。响应继续引用 `InferenceServiceEngine`，其 `command` 仍为数组。`engine.env`、省略命令时的默认启动行为及 PATCH 范围保持原有语义。

Gateway 负责解析，gRPC、数据库 `desired_spec.engine.command`、运行时继续使用 `[]string`。不新增数据库列，不修改全局 dtype、不部署集群或改变当前运行中的服务。

## 文本语法

- 空格、制表符分隔参数；单引号、双引号保留参数内的空格；相邻的引用片段连接为同一参数。
- 单引号内所有字符为字面量；引用外反斜杠转义下一个字符。双引号内仅转义双引号、反斜杠、美元符号、反引号和换行，其余反斜杠保留。
- 反斜杠换行作为续行。未引用、未转义的换行和 `; & | < > ( )` 返回 400。未转义的美元符号和反引号在引用外或双引号内返回 400；单引号或转义后的字符保持字面量。
- 不运行 shell、不展开变量、glob 或命令替换。数组形式保留原有完整 argv 的能力。
- 空白命令、空参数、未闭合引号、末尾反斜杠、NUL、超过 64 个参数或单参数超过 4096 的输入返回 `400 INVALID_ARGUMENT`。字符串输入额外限定为最多 262144 字符（实现沿用既有 UTF-8 字节上限）。
- 解析后继续执行现有 env/command 验证，不新增 vLLM 专用逻辑。

## 验收

HTTP handler 测试证明普通文本、引用/转义、旧数组都转发为预期 argv；错误文本在调用 gRPC 前被拒绝。创建与 GET 回显保持数组。现有默认启动、reserved env、模型路径替换测试应继续通过。同步 Services 契约检查、SDK/API 文档生成物及批次记录，运行相关模块和仓库门禁；不声称此次文本输入已经 live。
