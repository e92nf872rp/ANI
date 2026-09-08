"""P1 RPC declarations (SPEC §4.1).

UpdateKBPermissions is declared in kb_service.proto but still returns
UNIMPLEMENTED. Kept in a separate module so the servicer (grpc_server.py)
can delegate to it without carrying P1 logic. ListKBCitations and
ListKBSessions were implemented in B2 (issue-045) directly in grpc_server.
"""
import grpc


def update_kb_permissions(request, context):
    context.abort(grpc.StatusCode.UNIMPLEMENTED, "UpdateKBPermissions is a P1 RPC, not implemented yet")
