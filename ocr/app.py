"""Pantry OCR sidecar.

Implements the OCR contract the Go API expects:

    POST /ocr      multipart "image" -> {engine, version, ms, width, height, lines[]}
    GET  /healthz  -> {engine, ready}

The engine is chosen with OCR_ENGINE. Only text + boxes come out; all
receipt understanding happens in Go.
"""

import io
import os
import threading
import time

import numpy as np
from fastapi import FastAPI, File, HTTPException, UploadFile
from PIL import Image, ImageOps

from engines import load_engine

ENGINE_NAME = os.environ.get("OCR_ENGINE", "ppocrv5")
MAX_SIDE = int(os.environ.get("OCR_MAX_SIDE", "2400"))

app = FastAPI(title="pantry-ocr")
_engine = None
_lock = threading.Lock()  # engines are not thread-safe; one image at a time


@app.on_event("startup")
def _load() -> None:
    global _engine
    _engine = load_engine(ENGINE_NAME)


@app.get("/healthz")
def healthz():
    return {"engine": ENGINE_NAME, "ready": _engine is not None}


@app.post("/ocr")
async def ocr(image: UploadFile = File(...)):
    if _engine is None:
        raise HTTPException(503, "engine loading")
    data = await image.read()
    try:
        img = Image.open(io.BytesIO(data))
        img = ImageOps.exif_transpose(img).convert("RGB")
    except Exception as exc:  # noqa: BLE001 - surface any decode failure as 400
        raise HTTPException(400, f"cannot decode image: {exc}") from exc

    img.thumbnail((MAX_SIDE, MAX_SIDE))
    arr = np.asarray(img)

    start = time.perf_counter()
    with _lock:
        lines = _engine.recognize(arr)
    ms = int((time.perf_counter() - start) * 1000)

    return {
        "engine": ENGINE_NAME,
        "version": _engine.version,
        "ms": ms,
        "width": img.width,
        "height": img.height,
        "lines": lines,
    }
