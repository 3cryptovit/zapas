"""Собирает practice-1-zapas.docx из README.md.

    python build-docx.py                  # pandoc ищется в PATH
    PANDOC=/path/to/pandoc python build-docx.py

Pandoc не задаёт размер страницы и сужает широкие картинки, поэтому после
него документ правится: лист A4, поля 30/15/20/20 мм, диаграмма во всю
ширину текста.
"""

import os
import pathlib
import re
import struct
import subprocess
import zipfile

HERE = pathlib.Path(__file__).parent
SRC = HERE / "README.md"
OUT = HERE / "practice-1-zapas.docx"
IMAGE = HERE / "use-case.png"

EMU_PER_TWIP = 635
PAGE_W, PAGE_H = 11906, 16838  # A4 в твипах
MARGIN = dict(top=1134, right=850, bottom=1134, left=1701, header=709, footer=709, gutter=0)
TEXT_W_EMU = (PAGE_W - MARGIN["left"] - MARGIN["right"]) * EMU_PER_TWIP

subprocess.run(
    [os.environ.get("PANDOC", "pandoc"), str(SRC), "-f", "markdown", "-t", "docx",
     "-M", "lang=ru-RU", "-o", str(OUT)],
    check=True, cwd=HERE,
)

with IMAGE.open("rb") as f:
    f.read(16)
    img_w, img_h = struct.unpack(">II", f.read(8))
cx = TEXT_W_EMU
cy = cx * img_h // img_w

tmp = OUT.with_suffix(".tmp")
with zipfile.ZipFile(OUT) as zi, zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zo:
    for item in zi.infolist():
        data = zi.read(item.filename)
        if item.filename == "word/document.xml":
            xml = data.decode("utf-8")
            margins = " ".join(f'w:{k}="{v}"' for k, v in MARGIN.items())
            page = f'<w:pgSz w:w="{PAGE_W}" w:h="{PAGE_H}" /><w:pgMar {margins} />'
            # в sectPr порядок строгий: pgSz и pgMar идут после footnotePr
            xml, n = re.subn(r"(</w:sectPr>)(?![\s\S]*</w:sectPr>)", page + r"\1", xml)
            assert n == 1, "не нашёл sectPr"
            # единственная картинка — диаграмма: extent и a:ext задают её размер
            xml, n = re.subn(r'(<(?:wp:extent|a:ext)) cx="\d+" cy="\d+"', rf'\1 cx="{cx}" cy="{cy}"', xml)
            assert n == 2, f"ожидал 2 размера картинки, нашёл {n}"
            data = xml.encode("utf-8")
        zo.writestr(item, data)
tmp.replace(OUT)
print(f"{OUT.name}: A4, диаграмма {cx / 360000:.1f} × {cy / 360000:.1f} см")
