"""Add object_id column to kb_documents (dev aid) + show storage_path of existing docs."""
import asyncio
import asyncpg

DSN = "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"


async def main():
    conn = await asyncpg.connect(dsn=DSN)
    try:
        await conn.execute("ALTER TABLE kb_documents ADD COLUMN IF NOT EXISTS object_id TEXT")
        print("object_id column ensured.")
        rows = await conn.fetch(
            "SELECT id, kb_id, storage_path, parse_status FROM kb_documents"
        )
        for r in rows:
            print(f"  doc={r['id']} kb={r['kb_id']} status={r['parse_status']} path={r['storage_path']}")
    finally:
        await conn.close()


if __name__ == "__main__":
    asyncio.run(main())
