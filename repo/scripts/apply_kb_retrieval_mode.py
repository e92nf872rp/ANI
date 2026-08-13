"""Apply the knowledge_bases.retrieval_mode column (003 migration) to the DB."""
import asyncio
import asyncpg

DSN = "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"

SQL = """
ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS retrieval_mode TEXT NOT NULL DEFAULT 'hybrid'
        CHECK (retrieval_mode IN ('vector', 'hybrid', 'keyword'));
"""


async def main() -> None:
    conn = await asyncpg.connect(dsn=DSN)
    try:
        await conn.execute(SQL)
        print("retrieval_mode column applied")
    finally:
        await conn.close()


if __name__ == "__main__":
    asyncio.run(main())
