"""Deterministic Office rendering. Input is validated data, never Python or shell."""
import datetime
import io
import math
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import zipfile

from docx import Document
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Inches as DocInches, Pt as DocPt, RGBColor as DocColor
from openpyxl import Workbook, load_workbook
from openpyxl.chart import BarChart, LineChart, Reference
from openpyxl.styles import Alignment, Font, PatternFill, Border, Side
from openpyxl.worksheet.table import Table, TableStyleInfo
from openpyxl.utils import get_column_letter
from pptx import Presentation
from pptx.chart.data import CategoryChartData
from pptx.dml.color import RGBColor
from pptx.enum.chart import XL_CHART_TYPE
from pptx.opc.constants import RELATIONSHIP_TYPE as PPT_REL
from pptx.util import Inches, Pt

from schema import FORMATS, InvalidSpec, MAX_RESULT_BYTES, validate_spec

FONT = "Noto Sans CJK SC"
MIMES = {
    "slides": ("pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"),
    "document": ("docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
    "spreadsheet": ("xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
}
COLORS = {"mono": "18181B", "blue": "1D4ED8", "warm": "9A3412"}


def _ppt_text(slide, value, x, y, width, height, size, *, bold=False, color="18181B"):
    shape = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(width), Inches(height))
    frame = shape.text_frame
    frame.word_wrap = True
    frame.margin_left = frame.margin_right = Inches(.02)
    frame.margin_top = frame.margin_bottom = Inches(.02)
    frame.text = value
    for paragraph in frame.paragraphs:
        paragraph.font.name = FONT
        paragraph.font.size = Pt(size)
        paragraph.font.bold = bold
        paragraph.font.color.rgb = RGBColor.from_string(color)
        paragraph.space_after = Pt(8)
    return shape


def render_slides(spec, path):
    prs = Presentation()
    # The library's blank template carries a binary printer-driver setting.
    # It is unrelated to generated content; do not distribute machine-specific
    # binary parts or weaken the gateway's data-only archive policy for it.
    for relationship in list(prs.part.rels.values()):
        if relationship.reltype == PPT_REL.PRINTER_SETTINGS:
            prs.part.drop_rel(relationship.rId)
    prs.slide_width, prs.slide_height = Inches(13.333333), Inches(7.5)
    prs.core_properties.title, prs.core_properties.author = spec["title"], "SubAPIs"
    accent = COLORS[spec.get("theme", "mono")]
    for index, item in enumerate(spec["slides"]):
        slide = prs.slides.add_slide(prs.slide_layouts[6])
        slide.background.fill.solid()
        slide.background.fill.fore_color.rgb = RGBColor(255, 255, 255)
        layout = item["layout"]
        if layout == "cover":
            _ppt_text(slide, item["title"], .9, 2, 11.5, 2.15, 44, bold=True, color=accent)
            _ppt_text(slide, item.get("subtitle", ""), .95, 4.55, 11.3, 1.3, 22, color="52525B")
        else:
            _ppt_text(slide, item["title"], .8, .4, 11.7, 1.35, 32, bold=True, color=accent)
            if layout == "bullets":
                y = 1.85
                for bullet in item["bullets"]:
                    # One native editable paragraph per point, never a screenshot.
                    box = _ppt_text(slide, "• " + bullet, 1, y, 11.2, .75, 22)
                    for paragraph in box.text_frame.paragraphs:
                        paragraph.space_after, paragraph.line_spacing = Pt(0), 1.0
                    y += .8
            elif layout == "table":
                headers, rows = item["table"]["headers"], item["table"]["rows"]
                table = slide.shapes.add_table(len(rows)+1, len(headers), Inches(.85), Inches(1.85), Inches(11.65), Inches(4.7)).table
                for r, values in enumerate([headers] + rows):
                    for c, value in enumerate(values):
                        cell = table.cell(r, c)
                        cell.text = value
                        cell.margin_left = cell.margin_right = Inches(.1)
                        cell.margin_top = cell.margin_bottom = Inches(.05)
                        cell.fill.solid()
                        cell.fill.fore_color.rgb = RGBColor.from_string(accent if r == 0 else ("F4F4F5" if r % 2 else "FFFFFF"))
                        for p in cell.text_frame.paragraphs:
                            p.font.name, p.font.size, p.font.bold = FONT, Pt(17), r == 0
                            p.font.color.rgb = RGBColor.from_string("FFFFFF" if r == 0 else "18181B")
                            p.space_after, p.line_spacing = Pt(0), 1.0
            elif layout == "chart":
                data = CategoryChartData()
                chart_spec = item["chart"]
                data.categories = chart_spec["categories"]
                for series in chart_spec["series"]:
                    data.add_series(series["name"], series["values"])
                kind = XL_CHART_TYPE.COLUMN_CLUSTERED if chart_spec["type"] == "bar" else XL_CHART_TYPE.LINE
                chart = slide.shapes.add_chart(kind, Inches(.9), Inches(1.9), Inches(11.5), Inches(4.65), data).chart
                chart.has_title = bool(chart_spec["title"])
                chart.chart_title.text_frame.text = chart_spec["title"]
                chart.has_legend = len(chart_spec["series"]) > 1
                chart.category_axis.tick_labels.font.name = FONT
                chart.category_axis.tick_labels.font.size = Pt(16)
                chart.value_axis.tick_labels.font.size = Pt(16)
        _ppt_text(slide, str(index+1), 11.9, 7.02, .5, .25, 11, color="71717A")
        if item.get("notes"):
            slide.notes_slide.notes_text_frame.text = item["notes"]
    prs.save(path)
    reopened = Presentation(path)
    if len(reopened.slides) != len(spec["slides"]):
        raise InvalidSpec("Presentation validation failed")


def _doc_style(doc):
    section = doc.sections[0]
    section.page_width, section.page_height = DocInches(8.5), DocInches(11)
    section.top_margin = section.bottom_margin = DocInches(.8)
    section.left_margin = section.right_margin = DocInches(.85)
    for name, size in (("Normal", 11), ("Title", 26), ("Heading 1", 17), ("Heading 2", 13), ("List Bullet", 11)):
        style = doc.styles[name]
        style.font.name, style.font.size = FONT, DocPt(size)
        style.font.color.rgb = DocColor(0, 0, 0)
        style.element.get_or_add_rPr().get_or_add_rFonts().set(qn("w:eastAsia"), FONT)
        style.paragraph_format.space_after = DocPt(8)
        style.paragraph_format.line_spacing = 1.2
        properties = style.element.find(qn("w:pPr"))
        if properties is not None:
            for border in list(properties.findall(qn("w:pBdr"))):
                properties.remove(border)
    doc.styles["Title"].paragraph_format.space_after = DocPt(16)


def render_document(spec, path):
    doc = Document()
    _doc_style(doc)
    doc.core_properties.title, doc.core_properties.author = spec["title"], "SubAPIs"
    doc.add_paragraph(spec["title"], "Title")
    for section in spec["sections"]:
        doc.add_heading(section["heading"], 1)
        for paragraph in section.get("paragraphs", []):
            doc.add_paragraph(paragraph)
        for bullet in section.get("bullets", []):
            doc.add_paragraph(bullet, "List Bullet")
        if "table" not in section:
            continue
        data = section["table"]
        table = doc.add_table(rows=1, cols=len(data["headers"]))
        table.style = "Table Grid"
        borders = OxmlElement("w:tblBorders")
        for edge in ("top", "left", "bottom", "right", "insideH", "insideV"):
            b = OxmlElement("w:" + edge)
            for key, value in (("val", "single"), ("sz", "4"), ("color", "D9D9D9")):
                b.set(qn("w:" + key), value)
            borders.append(b)
        table._tbl.tblPr.append(borders)
        for i, header in enumerate(data["headers"]):
            table.rows[0].cells[i].text = header
        table.rows[0]._tr.get_or_add_trPr().append(OxmlElement("w:tblHeader"))
        for values in data["rows"]:
            cells = table.add_row().cells
            for c, value in enumerate(values):
                cells[c].text = value
        for r, row in enumerate(table.rows):
            for cell in row.cells:
                cell.vertical_alignment = 1
                if r == 0:
                    shade = OxmlElement("w:shd")
                    shade.set(qn("w:fill"), "F1F1F1")
                    cell._tc.get_or_add_tcPr().append(shade)
                for p in cell.paragraphs:
                    p.paragraph_format.space_after = DocPt(5)
                    p.paragraph_format.space_before = DocPt(5)
                    for run in p.runs:
                        run.font.size, run.bold = DocPt(10.5), r == 0
        doc.add_paragraph()
    doc.save(path)
    Document(path)  # Native parse smoke, followed by the mandatory PDF render.


def render_spreadsheet(spec, path):
    wb = Workbook()
    wb.remove(wb.active)
    wb.properties.title, wb.properties.creator = spec["title"], "SubAPIs"
    edge = Side(style="thin", color="E4E4E7")
    for index, item in enumerate(spec["sheets"]):
        ws = wb.create_sheet(item["name"])
        ws.sheet_view.showGridLines = False
        ws.freeze_panes = "A2"
        for col, column in enumerate(item["columns"], 1):
            cell = ws.cell(1, col, column["name"])
            cell.font = Font(name=FONT, size=11, bold=True, color="FFFFFF")
            cell.fill = PatternFill("solid", fgColor="18181B")
            cell.alignment = Alignment(vertical="center", wrap_text=True)
            ws.column_dimensions[get_column_letter(col)].width = 24 if column.get("format", "text") == "text" else 16
        header_height = max(32, max(16 * math.ceil(sum(2 if ord(ch)>255 else 1 for ch in col["name"]) / ws.column_dimensions[get_column_letter(i+1)].width) for i,col in enumerate(item["columns"])))
        ws.row_dimensions[1].height = header_height
        for r, row in enumerate(item["rows"], 2):
            height = 24
            for c, value in enumerate(row, 1):
                cell = ws.cell(r, c)
                cell.number_format = FORMATS[item["columns"][c-1].get("format", "text")]
                if isinstance(value, dict) and "formula" in value:
                    cell.value = value["formula"]
                elif isinstance(value, dict):
                    cell.value = datetime.date.fromisoformat(value["date"])
                    cell.number_format = FORMATS["date"]
                else:
                    cell.value = value
                    if isinstance(value, str):
                        cell.data_type = "s"  # "=..." in imported text must never become a formula.
                        units = sum(2 if ord(ch) > 255 else 1 for ch in value)
                        height = max(height, 16 * (math.ceil(units / ws.column_dimensions[get_column_letter(c)].width) + value.count("\n")))
                cell.font = Font(name=FONT, size=11, color="18181B")
                cell.alignment = Alignment(wrap_text=True, vertical="center")
                cell.border = Border(bottom=edge)
            if height > 400:
                raise InvalidSpec("Cell text is too tall; split it into multiple rows")
            ws.row_dimensions[r].height = height
        end = get_column_letter(len(item["columns"])) + str(len(item["rows"]) + 1)
        table = Table(displayName="Data" + str(index+1), ref="A1:" + end)
        table.tableStyleInfo = TableStyleInfo(name="TableStyleMedium2", showRowStripes=True)
        ws.add_table(table)
        last_row = len(item["rows"]) + 1
        if "chart" in item:
            cfg = item["chart"]
            chart = BarChart() if cfg["type"] == "bar" else LineChart()
            chart.title, chart.width, chart.height = cfg["title"], 16, 8
            for col in cfg["value_columns"]:
                chart.add_data(Reference(ws, min_col=col+1, min_row=1, max_row=last_row), titles_from_data=True)
            chart.set_categories(Reference(ws, min_col=cfg["category_column"]+1, min_row=2, max_row=last_row))
            ws.add_chart(chart, "A" + str(last_row+3))
            last_row += 20
        ws.print_area = "A1:" + get_column_letter(max(len(item["columns"]), 8 if "chart" in item else 1)) + str(last_row)
        ws.print_title_rows = "1:1"
        ws.page_setup.orientation = "landscape" if len(item["columns"]) > 5 else "portrait"
        ws.sheet_properties.pageSetUpPr.fitToPage = len(item["columns"]) <= 8
        ws.page_setup.fitToWidth, ws.page_setup.fitToHeight = 1, 0
    wb.save(path)
    load_workbook(path).close()


def convert_office(source, out_dir, file_format, profile):
    binary = os.environ.get("OFFICE_BINARY", "/usr/bin/libreoffice")
    command = [binary, "-env:UserInstallation=" + profile.as_uri(), "--headless", "--nologo",
               "--nodefault", "--nofirststartwizard", "--convert-to", file_format,
               "--outdir", str(out_dir), str(source)]
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        process.communicate(timeout=90)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.communicate()
        raise InvalidSpec("Office rendering timed out")
    output = out_dir / (source.stem + "." + file_format.split(":")[0])
    if process.returncode != 0 or not output.is_file() or output.stat().st_size == 0:
        raise InvalidSpec("Office rendering failed")
    return output


def render(spec, *, preview=True):
    validate_spec(spec)
    kind = spec["kind"]
    ext, mime = MIMES[kind]
    with tempfile.TemporaryDirectory(prefix="web-agent-office-") as directory:
        root = Path(directory)
        source = root / ("artifact." + ext)
        {"slides": render_slides, "document": render_document, "spreadsheet": render_spreadsheet}[kind](spec, source)
        if kind == "spreadsheet":
            recalculated = root / "calculated"
            recalculated.mkdir()
            source = convert_office(source, recalculated, "xlsx", root / "calc-profile")
            cached = load_workbook(source, data_only=True)
            try:
                for ws in cached:
                    for row in ws:
                        if any(cell.data_type == "e" for cell in row):
                            raise InvalidSpec("Spreadsheet contains a calculation error")
            finally:
                cached.close()
        pdf = b""
        if preview:
            preview_dir = root / "preview"
            preview_dir.mkdir()
            preview_path = convert_office(source, preview_dir, "pdf", root / "preview-profile")
            if preview_path.stat().st_size > MAX_RESULT_BYTES:
                raise InvalidSpec("Preview is too large")
            pdf = preview_path.read_bytes()
        if source.stat().st_size + len(pdf) > MAX_RESULT_BYTES:
            raise InvalidSpec("Artifact is too large")
        data = source.read_bytes()
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            if archive.testzip() is not None:
                raise InvalidSpec("Invalid Office archive")
        return {"extension": ext, "mime": mime, "file": data, "preview": pdf}
