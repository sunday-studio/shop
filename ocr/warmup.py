"""Build-time step: instantiate the engine so model weights are baked into the image."""

import os

import numpy as np

from engines import load_engine

engine = load_engine(os.environ.get("OCR_ENGINE", "ppocrv5"))
engine.recognize(np.full((64, 256, 3), 255, dtype=np.uint8))
print("warmup ok:", engine.version)
