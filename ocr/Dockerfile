FROM python:3.11-slim

# One image per engine: docker build --build-arg OCR_ENGINE=tesseract ...
ARG OCR_ENGINE=ppocrv5

RUN apt-get update \
 && apt-get install -y --no-install-recommends libgl1 libglib2.0-0 libgomp1 curl \
 && if [ "$OCR_ENGINE" = "tesseract" ]; then \
      apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-nld tesseract-ocr-eng; \
    fi \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements-*.txt ./
RUN if [ "$OCR_ENGINE" = "doctr" ]; then \
      pip install --no-cache-dir torch torchvision --index-url https://download.pytorch.org/whl/cpu; \
    fi \
 && pip install --no-cache-dir -r requirements-base.txt -r requirements-${OCR_ENGINE}.txt

ENV OCR_ENGINE=${OCR_ENGINE} \
    PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True \
    PYTHONUNBUFFERED=1

COPY engines.py warmup.py app.py ./
# Download weights at build time so the container starts offline.
RUN python warmup.py

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s \
  CMD curl -fsS http://localhost:8000/healthz || exit 1
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
