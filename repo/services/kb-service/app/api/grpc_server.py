"""KBService gRPC servicer (SPEC §2.4, §4.1).

This is the P0 skeleton: the server starts and responds to all 13 RPCs.
The 10 P0 RPCs return UNIMPLEMENTED here; business logic (repositories,
Core API client, rag-engine client, outbox, Redis session) is wired in
follow-up issues (US-009 repositories+clients, US-010 outbox+session).
The 3 P1 RPCs always return UNIMPLEMENTED (see p1_rpcs.py).
"""
import grpc

from app.api import p1_rpcs
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc


_P0_UNIMPLEMENTED = grpc.StatusCode.UNIMPLEMENTED


class KBServiceServicer(pb_grpc.KBServiceServicer):
    """P0 skeleton servicer for KBService (10 P0 RPCs + 3 P1 UNIMPLEMENTED)."""

    # ── 10 P0 RPCs (skeleton; wired in US-009 / US-010) ───────────────────────

    def CreateKB(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "CreateKB skeleton: repositories wired in US-009")

    def GetKB(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "GetKB skeleton: repositories wired in US-009")

    def ListKBs(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "ListKBs skeleton: repositories wired in US-009")

    def DeleteKB(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "DeleteKB skeleton: repositories wired in US-009")

    def GetDocumentUploadURL(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "GetDocumentUploadURL skeleton: Core API client wired in US-009")

    def NotifyDocumentUploaded(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "NotifyDocumentUploaded skeleton: outbox wired in US-010")

    def GetDocument(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "GetDocument skeleton: repositories wired in US-009")

    def ListDocuments(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "ListDocuments skeleton: repositories wired in US-009")

    def DeleteDocument(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "DeleteDocument skeleton: repositories wired in US-009")

    def Query(self, request, context):
        context.abort(_P0_UNIMPLEMENTED, "Query skeleton: rag-engine client + session wired in US-009/US-010")

    # ── 3 P1 RPC declarations (always UNIMPLEMENTED in P0) ────────────────────

    def ListKBCitations(self, request, context):
        return p1_rpcs.list_kb_citations(request, context)

    def ListKBSessions(self, request, context):
        return p1_rpcs.list_kb_sessions(request, context)

    def UpdateKBPermissions(self, request, context):
        return p1_rpcs.update_kb_permissions(request, context)
