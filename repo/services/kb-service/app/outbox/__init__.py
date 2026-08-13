"""kb-service outbox package (SPEC §2.4).

Owns the polling dispatcher that publishes outbox_events to NATS
`ani.tasks.kb.parse` (US-010, SPEC §6.1 dispatcher algorithm).
"""
