"""OCR engine adapters. Each returns [{text, conf, box:[x0,y0,x1,y1]}]."""

import os


class PPOCRv5:
    """PaddleOCR 3.x pipeline with PP-OCRv5 detection + latin recognition."""

    def __init__(self) -> None:
        import paddleocr
        from paddleocr import PaddleOCR

        self.version = f"paddleocr-{paddleocr.__version__}"
        self._ocr = PaddleOCR(
            text_detection_model_name=os.environ.get("OCR_DET_MODEL", "PP-OCRv5_mobile_det"),
            text_recognition_model_name=os.environ.get("OCR_REC_MODEL", "latin_PP-OCRv5_mobile_rec"),
            # Receipts arrive cropped and deskewed by the iOS document scanner.
            use_doc_orientation_classify=False,
            use_doc_unwarping=False,
            use_textline_orientation=False,
        )

    def recognize(self, img):
        out = []
        for res in self._ocr.predict(img):
            for text, score, box in zip(res["rec_texts"], res["rec_scores"], res["rec_boxes"]):
                text = text.strip()
                if not text:
                    continue
                out.append({
                    "text": text,
                    "conf": round(float(score), 4),
                    "box": [float(v) for v in box],
                })
        return out


ENGINES = {
    "ppocrv5": PPOCRv5,
}


def load_engine(name: str):
    try:
        return ENGINES[name]()
    except KeyError:
        raise SystemExit(f"unknown OCR_ENGINE {name!r}; known: {', '.join(ENGINES)}")
