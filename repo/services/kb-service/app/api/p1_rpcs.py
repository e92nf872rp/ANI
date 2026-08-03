"""Phase A P1 RPC declarations (SPEC §4.1).

These 3 RPCs are declared in kb_service.proto but return UNIMPLEMENTED in P0.
They will be implemented in a later phase. Kept in a separate module so the
P0 servicer (grpc_server.py) can delegate to them without carrying P1 logic.
"""
import grpc


def list_kb_citations(request, context):
    context.abort(grpc.StatusCode.UNIMPLEMENTED, "ListKBCitations is a P1 RPC, not implemented in P0")


def list_kb_sessions(request, context):
    context.abort(grpc.StatusCode.UNIMPLEMENTED, "ListKBSessions is a P1 RPC, not implemented in P0")


def update_kb_permissions(request, context):
    context.abort(grpc.StatusCode.UNIMPLEMENTED, "UpdateKBPermissions is a P1 RPC, not implemented in P0")
