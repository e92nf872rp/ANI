"""Debug: check median size calculation."""
import os
import sys
import tempfile
from pathlib import Path

os.chdir(Path(__file__).resolve().parents[3])
sys.path.insert(0, ".")

import fitz

with tempfile.TemporaryDirectory() as tmpdir:
    pdf_path = os.path.join(tmpdir, "heading_test.pdf")
    doc = fitz.open()
    page = doc.new_page()
    page.insert_text((72, 72), "Chapter One", fontsize=20)
    page.insert_text((72, 100), "Intro paragraph.", fontsize=12)
    page.insert_text((72, 130), "Section A", fontsize=16)
    page.insert_text((72, 160), "Detail paragraph.", fontsize=12)
    doc.save(pdf_path)
    doc.close()

    doc = fitz.open(pdf_path)
    page = doc[0]
    d = page.get_text("dict")
    sizes = []
    for block in d.get("blocks", []):
        for line in block.get("lines", []):
            for span in line.get("spans", []):
                size = round(span.get("size", 12), 1)
                text = span.get("text", "")
                if size > 0:
                    sizes.append(size)
                print(f"  size={size} text={repr(text)}")
    sizes.sort()
    median = sizes[len(sizes) // 2]
    print(f"\nAll sizes: {sizes}")
    print(f"Median: {median}")
    print(f"2x median: {median * 2.0}")
    print(f"1.5x median: {median * 1.5}")
    print(f"1.25x median: {median * 1.25}")
    doc.close()
