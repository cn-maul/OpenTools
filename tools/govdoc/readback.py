"""Read a GovDoc export back with python-docx and print the normative values.

Independent of the renderer: this asserts what the file actually contains, not
what the Go code believes it wrote.
"""

import sys

from docx import Document

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"


def east_asia(run):
    rpr = run._element.rPr
    if rpr is None or rpr.rFonts is None:
        return None
    return rpr.rFonts.get(W + "eastAsia")


def leading(paragraph):
    spacing = paragraph.paragraph_format.line_spacing
    if spacing is None:
        return None
    return getattr(spacing, "pt", spacing)


document = Document(sys.argv[1])
section = document.sections[0]

print("page", round(section.page_width.mm, 1), round(section.page_height.mm, 1))
print(
    "margins",
    round(section.top_margin.mm, 1),
    round(section.bottom_margin.mm, 1),
    round(section.left_margin.mm, 1),
    round(section.right_margin.mm, 1),
)

for paragraph in document.paragraphs:
    text = paragraph.text.strip()
    if not text:
        continue
    run = paragraph.runs[0] if paragraph.runs else None
    indent = paragraph.paragraph_format.first_line_indent
    print(
        f"{text[:22]!r:26} font={east_asia(run) if run else None!s:12}"
        f" size={run.font.size.pt if run and run.font.size else None!s:5}"
        f" leading={leading(paragraph)!s:5}"
        f" indent={round(indent.pt, 1) if indent else None}"
    )
