# RAG-REFACTOR-STEP-8B E2E Report (issue #036)

**Generated:** 2026-08-25T11:05:30
**Branch:** refactor/architecture-compliance
**Tenant:** `00000000-0000-0000-0000-000000000002`
**Shared KB:** `b88f30d1-8a90-4c0e-a610-989bab254bd6`
**vLLM:** http://10.10.1.2:6080/v1 (Qwen3.8-27B-FP8)

**Summary:** 6/11 criteria passed

| Criterion | Status | Detail |
| --- | --- | --- |
| E2E-6 SSE sequence (token*→sources→done) | PASS | events=['token', 'token', 'token', 'token', 'token', 'token', 'token', 'token']... total=1266 |
| E2E-1 parse parity (kb_chunks rows old==new) | FAIL | old=0 new=12 |
| E2E-2 sources Jaccard > 90% | PASS | worst=1.000 avg=1.000 n=15 |
| E2E-3 answer non-empty rate matches | FAIL | old=0.33 new=1.00 n=15 |
| E2E-4 no-result three gates (①②tokens=0, ③ unreachable/unittest) | PASS | gate1=True gate2=True gate3(doc)=True |
| E2E-5 latency P99(new) < P99(old)×1.5 | PASS | P50 old=0ms new=859ms | P99 old=2114ms new=1830ms |
| E2E-10 prompt equivalence (system=DEFAULT_CONTEXT_TEMPLATE) | PASS | template_ok=True compact_ok=True semantic_ok=True files=1 |
| §0.2 equivalence (P50/P99 + accuracy + Jaccard) | FAIL | P50 0→859ms | P99 2114→1830ms | acc 0.33→1.00 | jac_avg=1.000 |
| E2E-7 delete + vector cleanup (kb_chunks + Milvus gone) | PASS | chunks before=12 after=0 | vectors before=0 after=-1 |
| E2E-8 multi-turn (history + user-twice) | FAIL | r1_ans=True r2_ans=True msgs=4 roles=['user', 'assistant', 'user', 'assistant'] user_twice=False |
| E2E-9 flag rollback (behavior unchanged) | FAIL | exception: <_InactiveRpcError of RPC that terminated with:
	status = StatusCode.UNAVAILABLE
	details = "rag-engine unavailable: rag-engine query failed: internal server error"
	debug_error_string = "UNAVAILABLE:rag-engine unavailable: rag-engine query failed: internal server error"
> |

## Measurements

- Old-path queries: 15 samples
- New-path queries: 15 samples
- P50 old=0ms new=859ms
- P99 old=2114ms new=1830ms

## Acceptance Criteria Mapping

- E2E-1 parse parity (kb_chunks rows old==new)
- E2E-2 sources Jaccard > 90% (3 retrieval modes)
- E2E-3 answer non-empty rate matches (±15%)
- E2E-4 no-result three gates (①②tokens=0, ③ unreachable/unit-tested)
- E2E-5 P99(new) < P99(old)×1.5
- E2E-6 SSE event sequence (token*→sources→done)
- E2E-7 delete doc + Milvus vector cleanup
- E2E-8 multi-turn (history + user-twice in Generate)
- E2E-9 flag rollback (behavior unchanged)
- E2E-10 prompt equivalence (system=DEFAULT_CONTEXT_TEMPLATE)
- §0.2 equivalence (P50/P99 + accuracy + Jaccard)
