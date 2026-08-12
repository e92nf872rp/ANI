"""P0 KB 接口全量端到端测试（真实文件 + 真实数据，Core API 下载，向量截断输出）。

覆盖 gateway 注册的全部 10 个 P0 知识库接口（SPEC §4.1）：
  1. POST   /svc/knowledge-bases                             (create)
  2. GET    /svc/knowledge-bases                             (list)
  3. GET    /svc/knowledge-bases/{kb_id}                     (get)
  4. DELETE /svc/knowledge-bases/{kb_id}                     (delete)
  5. GET    /svc/knowledge-bases/{kb_id}/documents           (list docs)
  6. POST   /svc/knowledge-bases/{kb_id}/documents           (upload -> GetDocumentUploadURL)
  7. DELETE /svc/knowledge-bases/{kb_id}/documents/{doc_id}  (delete doc)
  8. POST   .../documents/{doc_id}/notify-uploaded           (notify)
  9. POST   /svc/knowledge-bases/{kb_id}/query               (query, sync)
 10. GET    /svc/knowledge-bases/{kb_id}/query/stream        (query, SSE)

末尾附带 3 个 P1 接口（citations/sessions/permissions）验证 501 降级。

每个接口均打印「请求内容」与「响应内容」；响应中长度超限的数组/向量/长字符串会被截断，
避免刷出超长浮点向量。
"""
import json
import sys
import time
import uuid

import requests

GATEWAY = "http://localhost:8080"
TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"
HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}

# 截断阈值
MAX_ARR_LEN = 6        # 数组/向量最多显示 N 个元素
MAX_STR_LEN = 300      # 长字符串最多显示 N 个字符
MAX_SOURCES = 3        # sources 最多显示 N 条

STORE_MARK = "__STORE__"  # 存储占位标记，见 STORE_BACKREF


# ── 截断工具 ────────────────────────────────────────────────────────────────
def truncate_val(v, depth=0):
    """递归截断：数组/向量只保留前 MAX_ARR_LEN 个元素，长字符串截断，dict 递归。"""
    if isinstance(v, list):
        n = len(v)
        if n > MAX_ARR_LEN:
            head = [truncate_val(x, depth + 1) for x in v[:MAX_ARR_LEN]]
            return head + [f"...(共 {n} 项, 已截断 {n - MAX_ARR_LEN} 项)"]
        return [truncate_val(x, depth + 1) for x in v]
    if isinstance(v, dict):
        return {k: truncate_val(x, depth + 1) for k, x in v.items()}
    if isinstance(v, (float, int)):
        return v
    if isinstance(v, str):
        if len(v) > MAX_STR_LEN:
            return v[:MAX_STR_LEN] + f"...(截断, 原长 {len(v)})"
        return v
    return v


def truncate_sources(obj):
    """对 query 响应中的 sources 列表单独截断条数。"""
    if isinstance(obj, dict) and isinstance(obj.get("sources"), list):
        srcs = obj["sources"]
        if len(srcs) > MAX_SOURCES:
            obj = dict(obj)
            obj["sources"] = srcs[:MAX_SOURCES] + [
                {"...": f"共 {len(srcs)} 条 sources, 已省略 {len(srcs) - MAX_SOURCES} 条"}
            ]
    return obj


def _build_long_doc() -> str:
    """构造一个多章节、远超 chunk_size 的长文档（约 6000+ 字）。

    文档采用 ``## 标题`` 分段结构，便于验证 rag-engine「按标题分段 + 每块
    大小不超过 chunk 值」的切分逻辑。包含平台、GPU、作业调度、知识库、
    混合检索、RAG 问答、向量数据库、运维、安全、API 等多个主题，段落足够
    长以保证每章能切出多个分块。
    """
    intro = """# ANI 平台一体化研发与运营平台手册

ANI 是一个面向异构算力集群的一体化研发与运营平台，聚焦 GPU 算力调度与计算资源的精细化运营，
提供从资源纳管、作业调度、数据管理、模型服务到知识库与智能问答的完整技术栈。

本文档用于演示知识库文档的分块与检索效果，各章节围绕平台的不同能力展开详细说明，
内容足够长以便按标题分段产生多个分块，从而验证全文检索、向量检索与混合检索的召回效果。

"""
    chapters = [
        ("第一章 算力资源管理", [
            "算力资源管理是 ANI 平台的基础底座，负责统一纳管跨机房、跨地域的 GPU 计算集群。"
            "平台通过自研的调度器感知每台计算节点上的 GPU 型号、显存容量、显存带宽、PCIe 拓扑与网络带宽，"
            "形成一张全局的资源视图。管理员可以通过控制台实时查看各节点的负载、温度、功耗与健康状态，"
            "并在资源紧张时对租户进行限额与配额管理，确保核心业务优先获得算力。",
            "资源池抽象将物理节点划分为多个虚拟资源池，每个资源池可以绑定特定的机型、驱动版本或加速卡类型。"
            "调度器在分配作业时综合考虑资源亲和性、反亲和性、抢占优先级与成本因子，实现吞吐与延迟的平衡。"
            "平台还支持弹性伸缩，根据队列负载自动增减节点，在保障服务质量的同时降低闲置资源成本。",
        ]),
        ("第二章 作业调度系统", [
            "作业调度是 ANI 平台面向 AI 训练与推理的核心能力，支持多租户、分布式与异构 GPU 的大规模调度。"
            "系统实现了几类经典的调度算法：先来先服务、优先级抢占、公平共享、以及基于 DRF 的占优资源公平算法。"
            "对于大模型训练任务，调度器支持多机多卡的一体化分配，保证同一作业的所有任务能够同时获得所需的全部资源，"
            "避免因资源不足导致的训练中断。",
            "作业可分为批处理作业、交互式作业与长时间运行的推理服务。平台提供排队、暂停、恢复、取消与优先级调整等操作，"
            "并记录每个作业的资源使用曲线与费用结算明细。调度器与监控系统联动，当节点故障或作业异常退出时自动触发重调度，"
            "保障训练的连续性。",
        ]),
        ("第三章 对象存储与数据管理", [
            "数据管理模块基于对象存储为知识库与数据集提供持久化能力，默认底层采用与 S3 协议兼容的存储引擎。"
            "每个知识库对应一个独立的存储桶，桶内按文档目录组织对象，支持版本、生命周期与访问策略管理。"
            "文件上传采用预签名 URL 的方式直传对象存储，网关仅负责签发凭证，避免大文件在网关侧产生带宽瓶颈。",
            "平台为上传的每个文档计算 SHA-256 校验和，用于内容去重与完整性校验。文档解析完成后，"
            "原始文件保留在对象存储中，解析出的文本分块写入关系数据库，向量写入向量数据库，"
            "形成文件、正文、向量三层数据模型，供检索与问答链路消费。",
        ]),
        ("第四章 知识库与文档解析", [
            "知识库模块负责文档的上传、解析、清洗与分块。系统支持常见的办公文档、Markdown、HTML、PDF 与纯文本格式，"
            "对扫描件调用光学字符识别能力把图像中的文字提取出来。解析阶段会留存文档结构信息，例如标题层级、段落与列表，"
            "以便后续按语义边界执行分块。",
            "分块策略首先按标题进行分段，再对每个标题段落内部按设定的分块大小进一步切分，保证每个分块不超过配置的字符上限。"
            "分块时会将子块与其父块建立关联，并把父块内容冗余写入子块元数据，便于检索命中子块后回填父块的上下文。"
            "每个知识库可以独立配置分块大小、向量检索的返回条数与相似度阈值，这些参数在创建知识库时确定并从知识库信息中读取。",
        ]),
        ("第五章 向量检索", [
            "向量检索面向语义相似度，将查询文本与知识库分块分别编码为稠密向量后在向量数据库中检索最近邻。"
            "默认采用余弦相似度度量，返回分数越接近 1 表示语义越相关。向量数据库基于 HNSW 图索引结构，"
            "在大规模数据上也能保持亚毫秒级的检索延迟。",
            "向量化使用统一的文本嵌入模型，保证写入与查询使用同一套模型，从而保证向量空间的语义一致性。"
            "检索结果按相似度降序返回，并可以在知识库层面配置相似度阈值，只有达到阈值的分块才会被采纳为答案依据，"
            "低于阈值时视为无相关内容，避免模型基于不相关或低质量上下文生成幻觉回答。",
        ]),
        ("第六章 全文检索", [
            "全文检索基于关键词的文本字面相似度，使用数据库的 trigram 相似度算子对分块内容进行匹配与打分。"
            "它不依赖向量模型，因此对专有名词、编号、命令与精确短语的命中更为直接，适合用户使用确切关键词进行查询的场景。"
            "全文检索会优先返回与查询词高度字面重叠的分块，并实时计算相似度分数。",
            "为提升中文短查询的召回率，系统在检索时降低了内置的相似度阈值，避免短查询因 trigram 天然偏低而被整体过滤，"
            "最终的相关性判断交由知识库配置的分数阈值完成。全文检索与向量检索可以独立使用，也可以合并为混合检索。",
        ]),
        ("第七章 混合检索", [
            "混合检索同时调用向量检索与全文检索两条通路，并将二者的结果通过融合算法合并排序，"
            "从而兼顾语义相关与字面匹配两种信号。融合采用倒数排名融合，即根据每个文档在两个通路中的排名计算融合得分，"
            "排名靠前的文档获得更高加分，最终按融合得分从高到低返回。",
            "这种融合方式使得向量分与文本分两条量纲不同的分数能够公平地参与排序，避免数值较大的一侧完全主导结果。"
            "混合检索的相似度判定以向量通路的语义相似度为准，保证分数语义与向量检索保持一致，"
            "所有返回的分块分数均为归一到 0 到 1 的相似度，便于前端展示与比较。",
        ]),
        ("第八章 RAG 问答", [
            "检索增强生成问答基于检索到的知识库分块构造上下文，并交给大语言模型生成带依据的回答。"
            "系统首先按所选检索方式召回相关分块，再把分块内容与用户问题组装成提示词，调用 vLLM 提供的大语言模型完成生成。"
            "回答同步返回命中的分块列表与其相似度分数，形成可追溯的引用，提升回答的可靠性与可解释性。",
            "问答支持多轮会话，会话记忆通过 Redis 保存，同一会话内可以连续追问。系统统计每次问答的输入与输出 token 消耗，"
            "用于成本核算与限流。当检索到的分块均低于相似度阈值时，系统返回明确的“未检索到相关内容”提示而不是强行作答，"
            "从机制上杜绝幻觉输出。",
        ]),
        ("第九章 运维与监控", [
            "运维模块为平台提供集群监控、日志、告警与容量规划能力。监控组件采集节点、GPU、作业与服务的指标，"
            "如 GPU 利用率、显存占用、作业排队时长与接口延迟，并通过看板进行可视化展示。"
            "当指标超过阈值时触发告警，通过邮件、企微等方式通知运维人员。",
            "平台提供完整审计日志，记录用户操作、权限变更与敏感数据访问，满足合规要求。"
            "容量规划模块根据历史使用趋势预测未来资源需求，辅助管理员进行扩缩容决策与成本估算。",
        ]),
        ("第十章 安全与多租户", [
            "安全模块覆盖认证、授权、传输加密与数据隔离。所有 API 均需通过网关进行身份校验，"
            "采用租户级行级安全策略，使不同租户的数据在数据库层面相互隔离，避免越权访问。"
            "文件存储遵从最小权限原则，上传与下载均需网关签发的临时凭证。",
            "多租户模型支持组织、项目与成员的层级结构，配额与计量均按租户维度进行，"
            "租户之间互不可见、互不影响，保障数据与资源的强隔离。",
        ]),
    ]
    parts = [intro]
    for title, paras in chapters:
        parts.append(f"## {title}\n\n")
        for p in paras:
            # 每个段落重复拼接，确保单章足够长以切出多个分块
            for _ in range(3):
                parts.append(p)
            parts.append("\n")
    return "\n".join(parts)


# ── 打印工具 ────────────────────────────────────────────────────────────────
def sep(title):
    print(f"\n{'=' * 78}\n  {title}\n{'=' * 78}")


def dump(label, obj):
    print(f"\n  ── {label} ──")
    print(json.dumps(obj, ensure_ascii=False, indent=2, default=str))


def block(label, body):
    print(f"\n  ── {label} ──")
    print(body)


def case(no, method, path, req=None, resp_status=200, body=None):
    """打印单个接口的调用摘要：方法与路径 + 请求体 + 状态码 + 截断后的响应体。"""
    print(f"\n  ▶ [{no}] {method} {path}")
    if req is not None:
        print(f"      REQUEST : {json.dumps(req, ensure_ascii=False, default=str)}")
    if body is not None:
        print(f"      RESPONSE({resp_status}) : {body}")


def trunc_print(no, method, path, req, resp):
    """打印一个完整接口的输入 + 输出（响应已截断向量/长字段）。"""
    r = resp
    try:
        data = r.json()
    except Exception:
        data = r.text
    data = truncate_val(truncate_sources(data))
    case(no, method, path, req, r.status_code, data)


# ── 主流程 ──────────────────────────────────────────────────────────────────
def main():
    summary = []
    kb_id = None
    doc_id = None

    def record(label, ok, detail=""):
        summary.append((label, ok, detail))
        mark = "PASS" if ok else "FAIL"
        print(f"  [{mark}] {label} {detail}")

    # 接受命令行传入的 chunk 值: python test_kb_p0_all.py [chunk_size]，默认 512。
    # 该值用于「创建知识库」并贯穿整条链路，从而验证 rag-engine 是否按所设 chunk 值切分。
    chunk_size = int(sys.argv[1]) if len(sys.argv) >= 2 and sys.argv[1].isdigit() else 512
    print(f"chunk_size = {chunk_size}  (创建知识库时设置)")

    sep("① P0-1  CreateKB   POST /svc/knowledge-bases")
    req = {
        "idempotency_key": f"p0_create_{uuid.uuid4()}",
        "name": f"p0-kb-{int(time.time())}",
        "description": "P0 全接口真实数据端到端测试知识库",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": chunk_size,
        "top_k": 5,
        "score_threshold": 0.3,
        "retrieval_mode": "hybrid",
    }
    r = requests.post(f"{GATEWAY}/api/v1/svc/knowledge-bases", headers=HEADERS, json=req, timeout=30)
    kb = r.json() if r.status_code in (200, 201) else {}
    kb_id = kb.get("id")
    kb_retrieved = r.status_code == 201 and kb.get("retrieval_mode") == "hybrid"
    trunc_print(1, "POST", "/svc/knowledge-bases", req, r)
    record("CreateKB", r.status_code == 201, f"kb_id={kb_id}")
    record("CreateKB 回传参数", kb_retrieved,
           f"retrieval_mode={kb.get('retrieval_mode')}, top_k={kb.get('top_k')}, "
           f"score_threshold={kb.get('score_threshold')}, chunk_size={kb.get('chunk_size')}")
    if r.status_code != 201:
        return 1

    sep("② P0-2  ListKB   GET /svc/knowledge-bases")
    req = {"limit": 20, "cursor": ""}
    r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases", headers=HEADERS, params={"limit": "20"}, timeout=30)
    raw = r.json()
    items_raw = raw.get("items", []) if isinstance(raw, dict) else []
    n = len(items_raw)
    member = kb_id in [i.get("id") for i in items_raw]
    data = truncate_val(raw)
    trunc_print(2, "GET", "/svc/knowledge-bases", req, r)
    record("ListKB", r.status_code == 200 and n >= 1, f"items={n}, 新 KB 在列表中? {member}")

    sep("③ P0-3  GetKB   GET /svc/knowledge-bases/{kb_id}")
    req = {"kb_id": kb_id}
    r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}", headers=HEADERS, timeout=30)
    trunc_print(3, "GET", f"/svc/knowledge-bases/{kb_id}", req, r)
    record("GetKB", r.status_code == 200 and r.json().get("id") == kb_id)

    sep("④ P0-4  UploadDocument(GetDocumentUploadURL)   POST /svc/knowledge-bases/{kb_id}/documents")
    # 长文档：多章节 + 远超 chunk_size 的字数（约 6000+ 字），以便真正测出
    # "按标题分段 + 每块大小 <= chunk 值" 的切分效果（文件太短测不出来）。
    file_name = "kb-p0-overview.md"
    content = _build_long_doc()
    content_bytes = content.encode("utf-8")
    req = {
        "idempotency_key": f"p0_upload_{uuid.uuid4()}",
        "file_name": file_name,
        "file_type": "md",
        "file_size_bytes": len(content_bytes),
        "checksum_sha256": __import__("hashlib").sha256(content_bytes).hexdigest(),
    }
    r = requests.post(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents", headers=HEADERS, json=req, timeout=30)
    body = r.json() if r.status_code == 200 else {}
    doc_id = body.get("doc_id")
    upload_url = body.get("upload_url")
    print(f"\n      REQUEST : {json.dumps(req, ensure_ascii=False)}")
    print(f"      RESPONSE({r.status_code}): doc_id={doc_id}")
    print(f"        storage_path = {body.get('storage_path')}")
    print(f"        upload_url   = {upload_url}")
    ok_real = upload_url and upload_url.startswith("http://10.10.1.66:30900")
    record("GetDocumentUploadURL", r.status_code == 200 and doc_id and ok_real,
           f"doc_id={doc_id}, 真实MinIO URL={ok_real}")

    sep("⑤ P0-4 附件- 真实文件上传到 MinIO (presigned PUT, Core 对象存储)")
    print(f"      上传文档字节数 = {len(content_bytes)} (长文档, 字数充足, 用于测出分块)")
    r = requests.put(upload_url, data=content_bytes,
                     headers={"Content-Type": "text/markdown"}, timeout=30)
    print(f"      PUT    {upload_url}")
    print(f"      RESPONSE({r.status_code}): {r.text[:120]}")
    record("真实文件 PUT 上传 MinIO", r.status_code == 200, f"status={r.status_code}, bytes={len(content_bytes)}")

    sep("⑥ P0-5  NotifyDocumentUploaded   POST .../documents/notify-uploaded")
    req = {"doc_id": doc_id, "storage_path": body.get("storage_path")}
    r = requests.post(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/notify-uploaded",
                      headers=HEADERS, json=req, timeout=30)
    trunc_print(6, "POST", f"/svc/knowledge-bases/{kb_id}/documents/notify-uploaded", req, r)
    record("NotifyDocumentUploaded", r.status_code == 202, "")

    sep("⑦ P0-6  ListDocuments(轮询解析)   GET /svc/knowledge-bases/{kb_id}/documents")
    req = {"limit": 20, "parse_status": ""}
    final_status, chunk_count = None, 0
    for i in range(24):
        time.sleep(5)
        r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                         headers=HEADERS, params={"limit": "20"}, timeout=30)
        if r.status_code != 200:
            continue
        items = r.json().get("items", [])
        if not items:
            continue
        d = items[0]
        final_status = d.get("parse_status")
        chunk_count = d.get("chunk_count") or 0
        print(f"      [poll {i}] parse_status={final_status} chunk_count={chunk_count}")
        if final_status in ("ready", "failed"):
            break
    trunc_print(7, "GET", f"/svc/knowledge-bases/{kb_id}/documents", req, r)
    record("ListDocuments", r.status_code == 200, f"items={len(r.json().get('items', []))}")
    record("解析终态", final_status in ("ready", "failed"), f"status={final_status}")
    record("解析==ready", final_status == "ready", f"status={final_status}")
    record("chunk_count>0", chunk_count > 0, f"chunk_count={chunk_count}")
    # 长文档应被切出多个分块：文件约 6000+ 字，chunk_size 默认 512，
    # 足以验证「按标题分段 + 每块 <= chunk 值」的效果。若 chunk_count 仍 <=1，
    # 说明解析/分块未真正生效（或文档内容过短）。
    record("长文档切出多分块", chunk_count > 1, f"chunk_count={chunk_count} (长文档验证分块)")

    sep("⑧ P0-7  Query(同步)   POST /svc/knowledge-bases/{kb_id}/query")
    # 多方面测试：分别以 KB 默认配置（hybrid）以及显式 vector / keyword
    # 三种检索方式查询同一长文档，输出各自的输入 + 响应（向量/长字段截断）。
    query_cases = [
        ("KB默认(hybrid)", None),
        ("vector", "vector"),
        ("keyword", "keyword"),
        ("hybrid显式", "hybrid"),
    ]
    for qlabel, mode in query_cases:
        req = {
            "idempotency_key": f"p0_query_{uuid.uuid4()}",
            "question": "ANI 平台的作业调度能力与混合检索原理是什么？",
            "top_k": 5,
            "score_threshold": 0.3,
        }
        if mode:
            req["retrieval_mode"] = mode
        sub = "  (显式 retrieval_mode=" + mode + ")" if mode else "  (使用KB默认检索方式)"
        print(f"\n      ▶ Query [{qlabel}]{sub}")
        print(f"      REQUEST : {json.dumps(req, ensure_ascii=False)}")
        r = requests.post(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
                          headers=HEADERS, json=req, timeout=120)
        raw = r.json() if r.status_code == 200 else {}
        q = truncate_val(truncate_sources(raw))
        print(f"      RESPONSE({r.status_code}):")
        print(json.dumps(q, ensure_ascii=False, indent=2, default=str))
        raw_sources = raw.get("sources") if isinstance(raw, dict) and raw.get("sources") is not None else []
        ns = len(raw_sources)
        # 校验：200 + 至少一条 source + source 内容非空（用截断前的原始数据）
        ok_src = False
        if r.status_code == 200 and isinstance(raw, dict) and raw_sources:
            ok_src = all((s.get("content") or "").strip() for s in raw_sources)
        record(f"Query[{qlabel}]", r.status_code == 200 and ns > 0 and ok_src,
               f"sources={ns}, 内容非空={ok_src}")

    sep("⑨ P0-8  Query(SSE)   GET /svc/knowledge-bases/{kb_id}/query/stream")
    r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
                     headers=HEADERS, params={"question": "ANI 平台的混合检索原理是什么？"}, stream=True, timeout=30)
    # text/event-stream 响应头未带 charset，requests 默认按 Latin-1 解码导致中文乱码；
    # 显式指定 UTF-8，保证 SSE 事件中的 delta/sources 中文正确显示。
    r.encoding = "utf-8"
    events = []
    body_str = ""
    for line in r.iter_lines(decode_unicode=True):
        if line:
            body_str += line + "\n"
            events.append(line)
    print(f"      RESPONSE({r.status_code}) SSE body:")
    # 打印 SSE body，但 token/delta 长内容截断
    print(body_str[:1200])
    has_sources = "event: sources" in body_str
    has_done = "event: done" in body_str
    n_tokens = body_str.count("event: token")
    record("SSE 200", r.status_code == 200)
    record("SSE 有 sources 事件", has_sources)
    record("SSE 有 done 事件", has_done, f"token 事件数={n_tokens}")

    sep("⑩ P0-9  DeleteDocument   DELETE /svc/knowledge-bases/{kb_id}/documents/{doc_id}")
    req = {"kb_id": kb_id, "doc_id": doc_id}
    r = requests.delete(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}",
                        headers=HEADERS, timeout=30)
    trunc_print(10, "DELETE", f"/svc/knowledge-bases/{kb_id}/documents/{doc_id}", req, r)
    record("DeleteDocument", r.status_code == 204)

    sep("⑪ P0-10  DeleteKB   DELETE /svc/knowledge-bases/{kb_id}")
    req = {"kb_id": kb_id}
    r = requests.delete(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}", headers=HEADERS, timeout=30)
    trunc_print(11, "DELETE", f"/svc/knowledge-bases/{kb_id}", req, r)
    record("DeleteKB", r.status_code == 204)

    sep("⑫ P1 对照  citations / sessions / permissions (预期 501)")
    r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/citations",
                     headers=HEADERS, params={"limit": "20"}, timeout=30)
    trunc_print(12, "GET", f"/svc/knowledge-bases/{kb_id}/citations", {"limit": 20}, r)
    record("ListKBCitations(P1)", r.status_code == 501, f"status={r.status_code}")
    r = requests.get(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/sessions",
                     headers=HEADERS, params={"limit": "20"}, timeout=30)
    trunc_print(13, "GET", f"/svc/knowledge-bases/{kb_id}/sessions", {"limit": 20}, r)
    record("ListKBSessions(P1)", r.status_code == 501, f"status={r.status_code}")
    r = requests.put(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/permissions",
                     headers=HEADERS, json={"idempotency_key": f"p1_{uuid.uuid4()}", "public_read": True},
                     timeout=30)
    trunc_print(14, "PUT", f"/svc/knowledge-bases/{kb_id}/permissions",
                {"idempotency_key": "p1", "public_read": True}, r)
    record("UpdateKBPermissions(P1)", r.status_code == 501, f"status={r.status_code}")

    sep("结果汇总")
    passed = sum(1 for _, ok, _ in summary if ok)
    failed = len(summary) - passed
    print(f"  通过: {passed}")
    print(f"  失败: {failed}")
    for label, ok, detail in summary:
        print(f"    [{'PASS' if ok else 'FAIL'}] {label} {detail}")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
