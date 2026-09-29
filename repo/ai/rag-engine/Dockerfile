# rag-engine — RAG core (parse/chunk/embed/retrieve/generate) FastAPI + gRPC
FROM python:3.11-slim

WORKDIR /app

# System deps for pymupdf / docling wheels
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc python3-dev libffi-dev \
 && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

EXPOSE 8001 50052

CMD ["python", "main.py"]
