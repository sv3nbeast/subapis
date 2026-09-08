"""Versioned, data-only Office tools. Unknown keys and executable inputs fail closed."""
import datetime
import math
import re
import unicodedata

from openpyxl.formula import Tokenizer
from openpyxl.utils.cell import range_boundaries

MAX_REQUEST_BYTES = 1_048_576
MAX_RESULT_BYTES = 32 * 1024 * 1024
FORMULAS = frozenset(("SUM", "AVERAGE", "MIN", "MAX", "COUNT", "COUNTA", "ROUND",
                      "IF", "IFERROR", "ABS", "SUMIFS", "COUNTIFS", "AND", "OR"))
FORMATS = {"text": "@", "integer": "#,##0", "decimal": "#,##0.00",
           "percent": "0.0%", "date": "yyyy-mm-dd",
           "usd": '"$"#,##0.00', "cny": '"¥"#,##0.00', "eur": '"€"#,##0.00'}


class InvalidSpec(ValueError):
    pass


def obj(value, required, optional=()):
    if not isinstance(value, dict) or not set(required).issubset(value) or set(value) - set(required) - set(optional):
        raise InvalidSpec("Missing or unsupported fields")
    return value


def text(value, limit, *, empty=False, display_limit=None):
    if not isinstance(value, str) or len(value) > limit or (not empty and not value.strip()):
        raise InvalidSpec("Invalid text length")
    if any((ord(c) < 32 and c not in "\n\t\r") or 0xD800 <= ord(c) <= 0xDFFF or c in "\ufffe\uffff" for c in value):
        raise InvalidSpec("Control characters are not allowed")
    if display_limit is not None:
        if any(c in value for c in "\n\r\t"):
            raise InvalidSpec("Slide text must use automatic wrapping")
        units = sum(2 if unicodedata.east_asian_width(c) in ("W", "F") else 1 for c in value)
        if units > display_limit:
            raise InvalidSpec("Content exceeds the layout budget; split it into more pages")
    return value


def array(value, minimum, maximum):
    if not isinstance(value, list) or not minimum <= len(value) <= maximum:
        raise InvalidSpec("Invalid number of items")
    return value


def number(value):
    if type(value) not in (int, float) or not math.isfinite(value) or abs(value) > 1e15:
        raise InvalidSpec("Invalid numeric value")
    return value


def validate_formula(formula, sheet_names):
    text(formula, 500)
    if not formula.startswith("=") or any(c in formula for c in ("[", "]", "|", "\n", "\r")):
        raise InvalidSpec("Invalid formula")
    try:
        tokens = Tokenizer(formula).items
    except Exception as exc:
        raise InvalidSpec("Invalid formula syntax") from exc
    for token in tokens:
        if token.type == "FUNC" and token.subtype == "OPEN":
            if token.value[:-1].upper() not in FORMULAS:
                raise InvalidSpec("Formula function is not allowed")
        elif token.type == "OPERAND" and token.subtype == "RANGE":
            value = token.value
            if "!" in value:
                sheet, value = value.rsplit("!", 1)
                if sheet.strip("'") not in sheet_names:
                    raise InvalidSpec("Formula refers to an unknown sheet")
            if not re.fullmatch(r"\$?[A-Z]{1,3}\$?[1-9][0-9]{0,4}(:\$?[A-Z]{1,3}\$?[1-9][0-9]{0,4})?", value):
                raise InvalidSpec("Only bounded cell references are allowed")
            _, _, max_col, max_row = range_boundaries(value)
            if max_col > 100 or max_row > 10000:
                raise InvalidSpec("Formula range is too large")
        elif token.type == "OPERAND" and token.subtype == "ERROR":
            raise InvalidSpec("Formula contains an error literal")
        elif token.type not in ("FUNC", "OPERAND", "OPERATOR-PREFIX", "OPERATOR-INFIX", "OPERATOR-POSTFIX", "PAREN", "SEP", "WHITE-SPACE"):
            raise InvalidSpec("Unsupported formula syntax")


def validate_chart(chart, *, spreadsheet=False, columns=0):
    required = ("type", "title", "category_column", "value_columns") if spreadsheet else ("type", "title", "categories", "series")
    obj(chart, required)
    if chart["type"] not in ("bar", "line"):
        raise InvalidSpec("Unsupported chart type")
    text(chart["title"], 80)
    if spreadsheet:
        indices = [chart["category_column"]] + array(chart["value_columns"], 1, 3)
        if any(type(i) is not int or not 0 <= i < columns for i in indices) or len(indices) != len(set(indices)):
            raise InvalidSpec("Invalid chart column")
    else:
        categories = array(chart["categories"], 1, 8)
        for category in categories:
            text(category, 16, display_limit=16)
        for series in array(chart["series"], 1, 3):
            obj(series, ("name", "values"))
            text(series["name"], 40)
            array(series["values"], len(categories), len(categories))
            for value in series["values"]:
                number(value)


def validate_spec(spec):
    obj(spec, ("kind", "title"), ("theme", "slides", "sections", "sheets"))
    kind = spec["kind"]
    if kind not in ("slides", "document", "spreadsheet"):
        raise InvalidSpec("Unsupported artifact kind")
    text(spec["title"], 120)
    if spec.get("theme", "mono") not in ("mono", "blue", "warm"):
        raise InvalidSpec("Unsupported theme")
    content_key = {"slides": "slides", "document": "sections", "spreadsheet": "sheets"}[kind]
    if content_key not in spec or any(k in spec for k in {"slides", "sections", "sheets"} - {content_key}):
        raise InvalidSpec("Artifact content does not match its kind")
    if kind == "slides":
        for slide in array(spec["slides"], 1, 40):
            obj(slide, ("layout", "title"), ("subtitle", "bullets", "table", "chart", "notes"))
            text(slide["title"], 80, display_limit=80)
            text(slide.get("notes", ""), 2000, empty=True)
            layout = slide["layout"]
            if layout not in ("cover", "bullets", "table", "chart"):
                raise InvalidSpec("Unsupported slide layout")
            allowed = {"cover": {"subtitle"}, "bullets": {"bullets"}, "table": {"table"}, "chart": {"chart"}}[layout]
            if set(slide) - {"layout", "title", "notes"} - allowed:
                raise InvalidSpec("Slide content does not match its layout")
            if layout == "cover":
                text(slide.get("subtitle", ""), 160, empty=True, display_limit=160)
            elif layout == "bullets":
                bullets = array(slide.get("bullets"), 1, 6)
                for bullet in bullets:
                    text(bullet, 90, display_limit=90)
                if sum(len(x) for x in bullets) > 400:
                    raise InvalidSpec("Too much slide text")
            elif layout == "table":
                table = obj(slide.get("table"), ("headers", "rows"))
                headers = array(table["headers"], 1, 5)
                for row in [headers] + array(table["rows"], 1, 7):
                    array(row, len(headers), len(headers))
                    for cell in row:
                        text(cell, 32, display_limit=32)
            else:
                validate_chart(slide.get("chart"))
    elif kind == "document":
        count = 0
        for section in array(spec["sections"], 1, 40):
            obj(section, ("heading",), ("paragraphs", "bullets", "table"))
            text(section["heading"], 120)
            if not any(section.get(k) for k in ("paragraphs", "bullets", "table")):
                raise InvalidSpec("Empty document section")
            for value in array(section.get("paragraphs", []), 0, 20) + array(section.get("bullets", []), 0, 20):
                count += len(text(value, 3000))
            if "table" in section:
                table = obj(section["table"], ("headers", "rows"))
                headers = array(table["headers"], 1, 8)
                for row in [headers] + array(table["rows"], 1, 100):
                    array(row, len(headers), len(headers))
                    for value in row:
                        count += len(text(value, 500))
        if count > 50000:
            raise InvalidSpec("Document is too large")
    else:
        sheets = array(spec["sheets"], 1, 8)
        names = []
        for sheet in sheets:
            obj(sheet, ("name", "columns", "rows"), ("chart",))
            name = text(sheet["name"], 31)
            if re.search(r"[\\/*?:\[\]']", name) or name != name.strip() or name.casefold() in [n.casefold() for n in names]:
                raise InvalidSpec("Invalid or duplicate sheet name")
            names.append(name)
        cells = 0
        for sheet in sheets:
            columns = array(sheet["columns"], 1, 20)
            column_names = set()
            for column in columns:
                obj(column, ("name",), ("format",))
                text(column["name"], 100)
                if column["name"].casefold() in column_names:
                    raise InvalidSpec("Duplicate column name")
                column_names.add(column["name"].casefold())
                if column.get("format", "text") not in FORMATS:
                    raise InvalidSpec("Unknown number format")
            for row in array(sheet["rows"], 1, 2000):
                array(row, len(columns), len(columns))
                cells += len(row)
                for value in row:
                    if value is None or type(value) is bool:
                        continue
                    if type(value) in (int, float):
                        number(value)
                    elif isinstance(value, str):
                        text(value, 1000, empty=True)
                    elif isinstance(value, dict) and set(value) == {"formula"}:
                        validate_formula(value["formula"], names)
                    elif isinstance(value, dict) and set(value) == {"date"}:
                        try:
                            datetime.date.fromisoformat(value["date"])
                        except (ValueError, TypeError):
                            raise InvalidSpec("Invalid date")
                    else:
                        raise InvalidSpec("Unsupported cell value")
            if "chart" in sheet:
                validate_chart(sheet["chart"], spreadsheet=True, columns=len(columns))
        if cells > 20000:
            raise InvalidSpec("Workbook contains too many cells")
    return spec
