"""Test if a real embedded query scores high against stored vectors."""
import requests
from pymilvus import connections, utility, Collection

QUERY_EMBED = "http://10.10.20.197:8006/v1/embeddings"
MODEL = "Qwen3-Embedding-0.6B"

# embed a real query using the same endpoint the app uses
r = requests.post(QUERY_EMBED, json={"model": MODEL, "input": ["ANI 平台提供哪些核心能力？"]}, timeout=30)
print("embedding status:", r.status_code)
data = r.json()
vec = data["data"][0]["embedding"]
print("query embedding dim:", len(vec))
print("first values:", vec[:5])
# normalize for cosine
import math
norm = math.sqrt(sum(v*v for v in vec))
print("norm:", round(norm, 3))

COLL = "kb_" + "e15dad09-144f-4d66-9aca-e2f2696e6709".replace("-", "")
connections.connect(alias="default", host="10.10.1.66", port="31930", timeout=10)
c = Collection(COLL)
c.load()
hits = c.search(data=[vec], anns_field="embedding", param={"metric_type": "COSINE", "params": {"ef": 64}}, limit=5, output_fields=["text"])
for h in hits:
    for x in h:
        print("HIT score=", round(x.distance, 4), "text=", str(x.entity.get("text"))[:50])
