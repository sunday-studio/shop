"""OCR engine adapters. Each returns [{text, conf, box:[x0,y0,x1,y1]}] in pixels.

Engines only produce text and boxes; all receipt understanding happens in Go.
"""

import os


class PPOCRv5:
    """PaddleOCR 3.x pipeline with PP-OCRv5 detection + recognition.

    Defaults to the mobile detector with the latin recogniser (accents);
    OCR_DET_MODEL / OCR_REC_MODEL select e.g. the server models.
    """

    def __init__(self) -> None:
        import paddleocr
        from paddleocr import PaddleOCR

        det = os.environ.get("OCR_DET_MODEL", "PP-OCRv5_mobile_det")
        rec = os.environ.get("OCR_REC_MODEL", "latin_PP-OCRv5_mobile_rec")
        self.version = f"paddleocr-{paddleocr.__version__} {det}+{rec}"
        self._ocr = PaddleOCR(
            text_detection_model_name=det,
            text_recognition_model_name=rec,
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
                if text:
                    out.append({"text": text, "conf": round(float(score), 4), "box": [float(v) for v in box]})
        return out


class Tesseract:
    """Tesseract 5 with Dutch + English; words grouped into its text lines."""

    def __init__(self) -> None:
        import pytesseract

        self._t = pytesseract
        self.lang = os.environ.get("OCR_TESSERACT_LANG", "nld+eng")
        # psm 4: a single column of text of variable sizes, which suits receipts.
        self.config = os.environ.get("OCR_TESSERACT_CONFIG", "--psm 4")
        self.version = f"tesseract-{pytesseract.get_tesseract_version()} {self.lang} {self.config}"

    def recognize(self, img):
        d = self._t.image_to_data(img, lang=self.lang, config=self.config, output_type=self._t.Output.DICT)
        lines = {}
        for i, word in enumerate(d["text"]):
            word = word.strip()
            conf = float(d["conf"][i])
            if not word or conf < 0:
                continue
            key = (d["block_num"][i], d["par_num"][i], d["line_num"][i])
            x0, y0 = d["left"][i], d["top"][i]
            x1, y1 = x0 + d["width"][i], y0 + d["height"][i]
            ln = lines.setdefault(key, {"words": [], "confs": [], "box": [x0, y0, x1, y1]})
            ln["words"].append(word)
            ln["confs"].append(conf / 100)
            b = ln["box"]
            ln["box"] = [min(b[0], x0), min(b[1], y0), max(b[2], x1), max(b[3], y1)]
        return [
            {"text": " ".join(ln["words"]), "conf": round(min(ln["confs"]), 4), "box": [float(v) for v in ln["box"]]}
            for ln in lines.values()
        ]


class DocTR:
    """docTR (PyTorch, CPU) detection + recognition; returns its text lines."""

    def __init__(self) -> None:
        import doctr
        from doctr.models import ocr_predictor

        det = os.environ.get("OCR_DOCTR_DET", "db_resnet50")
        rec = os.environ.get("OCR_DOCTR_REC", "parseq")
        self.version = f"doctr-{doctr.__version__} {det}+{rec}"
        self._model = ocr_predictor(det_arch=det, reco_arch=rec, pretrained=True,
                                    assume_straight_pages=True, preserve_aspect_ratio=True)

    def recognize(self, img):
        h, w = img.shape[:2]
        page = self._model([img]).export()["pages"][0]
        out = []
        for block in page["blocks"]:
            for line in block["lines"]:
                words = [wd for wd in line["words"] if wd["value"].strip()]
                if not words:
                    continue
                (x0, y0), (x1, y1) = line["geometry"]
                out.append({
                    "text": " ".join(wd["value"] for wd in words),
                    "conf": round(min(float(wd["confidence"]) for wd in words), 4),
                    "box": [x0 * w, y0 * h, x1 * w, y1 * h],
                })
        return out


class PaddleOCRVL:
    """PaddleOCR-VL (0.9B vision-language OCR model), used purely as OCR.

    It returns layout blocks rather than lines, so each block's text is
    split on newlines and the lines are spread evenly over the block's
    height. Slow on CPU; meant as an accuracy reference in the spike.
    """

    def __init__(self) -> None:
        import paddleocr
        from paddleocr import PaddleOCRVL as Pipeline

        self.version = f"paddleocr-vl paddleocr-{paddleocr.__version__}"
        self._p = Pipeline(use_doc_orientation_classify=False, use_doc_unwarping=False)

    def recognize(self, img):
        out = []
        for res in self._p.predict(img):
            data = res.json.get("res", res.json) if hasattr(res, "json") else res
            for block in data.get("parsing_res_list", []):
                text = (block.get("block_content") or "").strip()
                bbox = block.get("block_bbox")
                if not text or not bbox:
                    continue
                parts = [p.strip() for p in text.splitlines() if p.strip()]
                x0, y0, x1, y1 = [float(v) for v in bbox]
                step = (y1 - y0) / max(len(parts), 1)
                for i, p in enumerate(parts):
                    out.append({"text": p, "conf": 1.0, "box": [x0, y0 + i * step, x1, y0 + (i + 1) * step]})
        return out


ENGINES = {
    "ppocrv5": PPOCRv5,
    "tesseract": Tesseract,
    "doctr": DocTR,
    "paddleocr-vl": PaddleOCRVL,
}


def load_engine(name: str):
    try:
        cls = ENGINES[name]
    except KeyError:
        raise SystemExit(f"unknown OCR_ENGINE {name!r}; known: {', '.join(ENGINES)}")
    return cls()
