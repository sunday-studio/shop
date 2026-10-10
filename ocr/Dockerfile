FROM python:3.11-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends libgl1 libglib2.0-0 libgomp1 curl \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

ARG OCR_ENGINE=ppocrv5
ENV OCR_ENGINE=${OCR_ENGINE} \
    PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True \
    PYTHONUNBUFFERED=1

COPY engines.py warmup.py app.py ./
# Download weights at build time so the container starts offline.
RUN python warmup.py

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s \
  CMD curl -fsS http://localhost:8000/healthz || exit 1
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
