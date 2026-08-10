"""Verify PDF heading detection after _pdf_text_with_headings."""
import os
import sys
import tempfile
from pathlib import Path

os.chdir(Path(__file__).resolve().parents[3])
sys.path.insert(0, ".")

import fitz
from app.services.parse_service import ParseService

with tempfile.TemporaryDirectory() as tmpdir:
    pdf_path = os.path.join(tmpdir, "heading_test.pdf")
    doc = fitz.open()
    page = doc.new_page()
    # Body text fontsize=12, heading=20 (>= 2x), subheading=16 (>= 1.5x)
    page.insert_text((72, 72), "Chapter One", fontsize=20)
    page.insert_text((72, 100), "Intro paragraph.", fontsize=12)
    page.insert_text((72, 130), "Section A", fontsize=16)
    page.insert_text((72, 160), "Detail paragraph.", fontsize=12)
    doc.save(pdf_path)
    doc.close()

    svc = ParseService(uploader=None)
    nodes = svc.parse(pdf_path)
    print(f"PDF parsed nodes ({len(nodes)}):")
    for i, n in enumerate(nodes):
        sub = n.metadata.get("sub_type", "?")
        hl = n.metadata.get("heading_level", "-")
        sp = n.metadata.get("section_path", "")
        preview = n.content.replace("\n", "\\n")[:80]
        print(f"  Node[{i}] type={n.content_type} sub={sub} level={hl} path='{sp}' content: {preview}")

print("\nDONE")
