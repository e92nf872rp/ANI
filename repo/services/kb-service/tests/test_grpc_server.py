"""Tests for kb-service gRPC skeleton (issue-006 / US-008).

Verifies:
- 10 P0 RPCs are declared on the servicer and respond (UNIMPLEMENTED in skeleton).
- 3 P1 RPCs (ListKBCitations/ListKBSessions/UpdateKBPermissions) return UNIMPLEMENTED.
- gRPC server can start and respond to RPCs (AC: "gRPC server 可启动并响应 RPC").
"""
import os
import sys
import uuid
from concurrent import futures

import grpc
import pytest

# Make the kb-service package and generated stubs importable.
_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer
from app.generated.common.v1 import common_pb2
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as kb_grpc


# 10 P0 RPC names (SPEC §4.1) + 3 P1 RPC names.
P0_RPCS = [
    "CreateKB",
    "GetKB",
    "ListKBs",
    "DeleteKB",
    "GetDocumentUploadURL",
    "NotifyDocumentUploaded",
    "GetDocument",
    "ListDocuments",
    "DeleteDocument",
    "Query",
]
P1_RPCS = [
    "ListKBCitations",
    "ListKBSessions",
    "UpdateKBPermissions",
]


@pytest.fixture
def grpc_server():
    """Start an in-process gRPC server on an ephemeral port."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    kb_grpc.add_KBServiceServicer_to_server(KBServiceServicer(), server)
    port = server.add_insecure_port("[::]:0")
    server.start()
    yield f"localhost:{port}"
    server.stop(grace=None)


@pytest.fixture
def stub(grpc_server):
    return kb_grpc.KBServiceStub(grpc.insecure_channel(grpc_server))


# ── Servicer surface: all 13 RPCs are declared ──────────────────────────────

def test_servicer_declares_10_p0_rpcs():
    servicer = KBServiceServicer()
    for name in P0_RPCS:
        assert hasattr(servicer, name), f"servicer missing P0 RPC {name}"
        assert callable(getattr(servicer, name)), f"{name} is not callable"


def test_servicer_declares_3_p1_rpcs():
    servicer = KBServiceServicer()
    for name in P1_RPCS:
        assert hasattr(servicer, name), f"servicer missing P1 RPC {name}"
        assert callable(getattr(servicer, name)), f"{name} is not callable"


def test_servicer_subclasses_generated_base():
    assert isinstance(KBServiceServicer(), kb_grpc.KBServiceServicer)


# ── gRPC server can start and respond (AC: "gRPC server 可启动并响应 RPC") ──────
# AC5 is verified by the per-RPC tests below (each calls the stub and gets a
# real response from the in-process server started by the grpc_server fixture).


# ── 10 P0 RPCs respond (skeleton returns UNIMPLEMENTED) ─────────────────────

def test_create_kb_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.CreateKB(kb_pb.CreateKBRequest(tenant_id=str(uuid.uuid4()), name="kb1"))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_get_kb_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.GetKB(kb_pb.GetKBRequest(tenant_id="t", kb_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_list_kbs_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.ListKBs(kb_pb.ListKBsRequest(tenant_id="t", page=common_pb2.CursorPageRequest(limit=20)))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_delete_kb_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.DeleteKB(kb_pb.DeleteKBRequest(tenant_id="t", kb_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_get_document_upload_url_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.GetDocumentUploadURL(
            kb_pb.GetDocumentUploadURLRequest(
                tenant_id="t", kb_id=str(uuid.uuid4()), file_name="a.pdf",
                file_type="pdf", file_size_bytes=1024, idempotency_key=str(uuid.uuid4()),
            )
        )
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_notify_document_uploaded_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.NotifyDocumentUploaded(
            kb_pb.NotifyDocumentUploadedRequest(tenant_id="t", kb_id=str(uuid.uuid4()), doc_id=str(uuid.uuid4()))
        )
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_get_document_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.GetDocument(kb_pb.GetDocumentRequest(tenant_id="t", kb_id=str(uuid.uuid4()), doc_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_list_documents_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.ListDocuments(kb_pb.ListDocumentsRequest(tenant_id="t", kb_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_delete_document_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.DeleteDocument(kb_pb.DeleteDocumentRequest(tenant_id="t", kb_id=str(uuid.uuid4()), doc_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_query_skeleton_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.Query(
            kb_pb.QueryRequest(
                tenant_id="t", kb_id=str(uuid.uuid4()), question="hello",
                idempotency_key=str(uuid.uuid4()),
            )
        )
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


# ── 3 P1 RPCs return UNIMPLEMENTED (AC4) ─────────────────────────────────────

def test_list_kb_citations_p1_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.ListKBCitations(kb_pb.ListKBCitationsRequest(tenant_id="t", kb_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_list_kb_sessions_p1_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.ListKBSessions(kb_pb.ListKBSessionsRequest(tenant_id="t", kb_id=str(uuid.uuid4())))
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED


def test_update_kb_permissions_p1_unimplemented(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.UpdateKBPermissions(
            kb_pb.UpdateKBPermissionsRequest(
                tenant_id="t", kb_id=str(uuid.uuid4()), idempotency_key=str(uuid.uuid4()),
            )
        )
    assert exc.value.code() == grpc.StatusCode.UNIMPLEMENTED
