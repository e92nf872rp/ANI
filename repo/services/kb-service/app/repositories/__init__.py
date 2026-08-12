"""kb-service data access layer (SPEC §2.4, §4.2, §8.1).

All repositories use the Core data plane (`CoreClient.data_query`) for table
access. RLS tenant filtering is applied by Core based on the `X-Tenant-Id`
header carried by the CoreClient (role="tenant"); the outbox dispatcher uses
role="service" for cross-tenant access (SPEC §4.2).
"""
