"""Use-Case диаграмма сервиса Zapas для практической работы №1 (ВПК-7).

Раскладка ручная: автораскладчики (PlantUML/Graphviz) на 37 прецедентах
с перекрёстными связями дают нечитаемую путаницу. Координаты заданы явно,
а скрипт проверяет, что линии не задевают чужие эллипсы и акторов, а
подписи не наезжают на эллипсы, другие подписи и чужие линии.

    python use-case.py     # пишет use-case.svg и печатает отчёт проверки

PNG для отчёта снимается браузером без окна:

    chrome --headless=new --force-device-scale-factor=1.5 \
           --window-size=1720,2010 --screenshot=use-case.png use-case.svg
"""

import math
import pathlib
import sys
from xml.sax.saxutils import escape

OUT = pathlib.Path(__file__).with_name("use-case.svg")
W, H = 1720, 2010

FONT = "Segoe UI, Arial, sans-serif"
INK = "#1F2328"
MUTED = "#57606A"
LINE = "#3D444D"

# Колонки внутри системы: основные функции, затем внутренние действия.
M, I1, I2, I3 = 400, 730, 1040, 1340

# --- Прецеденты ---------------------------------------------------------
UC = {}


def uc(key, x, y, text, main=False, rx=None, ry=None):
    lines = text.split("\n")
    if rx is None:
        rx = 118 if main else 106
    if ry is None:
        ry = (31 if main else 27) + (len(lines) - 2) * 8
    UC[key] = dict(x=x, y=y, lines=lines, main=main, rx=rx, ry=ry)


# Локальный функционал
uc("L2", M, 135, "Открыть демо\nбез регистрации", True)
uc("L8", M, 220, "Прокрутить время\nв демо", True)
uc("L1", M, 310, "Войти в систему", True, ry=26)
uc("L3", M, 400, "Вести справочники:\nпозиции, поставщики", True)
uc("L5", M, 495, "Провести пересчёт\n(инвентаризацию)", True)
uc("L7", M, 600, "Принять поставку", True, ry=26)
uc("L4", M, 830, "Записать приход,\nрасход, списание", True)
uc("L6", M, 1005, "Оформить заказ\nпоставщику", True)

uc("l2", I1, 135, "Сгенерировать\nполгода истории")
uc("l1", I1, 310, "Проверить пароль,\nоткрыть сессию")
uc("l3", I1, 400, "Импортировать CSV\nс предпросмотром ошибок", rx=118)
uc("l8", I1, 495, "Провести корректировку\n«факт − учёт»", rx=112)
uc("l10", I1, 565, "Создать приходы\nпо факту")
uc("l11", I1, 635, "Зафиксировать\nрасхождение")
uc("l4", I1, 865, "Проверить ключ\nидемпотентности")
uc("l6", I1, 725, "Указать причину\nсписания")
uc("l7", I1, 795, "Сторнировать\nошибочное движение")
uc("l5", I1, 935, "Не допустить\nминусового остатка")
uc("l9", I2, 1005, "Сформировать\nтекст заявки")

# Собственное API
uc("A1", M, 1130, "Узнать, что заказать\nсегодня\nGET /dashboard", True, rx=128)
uc("A2", M, 1255, "Разобрать позицию\nи прогноз\nGET /items/{id}/insights", True, rx=140)
uc("A3", M, 1380, "Пересчитать прогнозы\n(ночью, по расписанию)", True, rx=128)

uc("a6", I1, 1130, "Пересчитать\nстатус позиции")
uc("a5", I2, 1130, "Рассчитать точку\nзаказа и количество")
uc("a4", I3, 1130, "Рассчитать окна\nпоставки d1, d2")
uc("a7", I1, 1210, "Объяснить статус\nпо шагам расчёта")
uc("a2", I2, 1270, "Построить прогноз\nна 14 дней (M1/M2)")
uc("a1", I3, 1215, "Собрать дневной\nряд расхода")
uc("a3", I3, 1325, "Оценить точность\nбэктестом (WAPE)")

# Внешние API
uc("E1", M, 1525, "Привязать\nTelegram-чат", True)
uc("E2", M, 1710, "Получать сводку\nи срочные алерты", True)

uc("e1", I1, 1490, "Выдать ссылку\nt.me/бот?start=код")
uc("e2", I1, 1560, "Принять вебхук,\nпроверить секрет")
uc("n1", I1, 1710, "Поставить в очередь\n(outbox)")
uc("e3", I2 + 40, 1635, "Отправить через\nsendMessage")
uc("e4", I2 + 40, 1795, "Отправить письмо\nпо SMTP")
uc("e5", I1, 1820, "Повторить доставку\n1 → 5 → 25 мин")

# --- Акторы: x, y — «пояс» фигуры ---------------------------------------
ACT = {
    "Guest": dict(x=185, y=165, lines=["Посетитель", "демо"]),
    "Staff": dict(x=95, y=560, lines=["Сотрудник"]),
    "Owner": dict(x=95, y=1100, lines=["Владелец"]),
    "Cron": dict(x=110, y=1540, lines=["Планировщик", "(воркер)"], stereo="система"),
    "TG": dict(x=1610, y=1600, lines=["Telegram", "Bot API"], stereo="внешняя система"),
    "SMTP": dict(x=1610, y=1815, lines=["SMTP-сервер"], stereo="внешняя система"),
}

# --- Рамки: (x0, y0, x1, y1, заголовок, заливка, цвет) ------------------
SYS = (240, 40, 1495, 1890)
GROUPS = [
    (262, 78, 1475, 1050, "Локальный функционал — внутри системы, без API-интеграций", "#F6F7F9", "#6E7781"),
    (262, 1070, 1475, 1435, "Собственное API Zapas — прогноз и рекомендация к заказу", "#E8F0FE", "#3B6FD4"),
    (262, 1455, 1475, 1870, "Внешние API — Telegram Bot API и SMTP", "#FDF0E4", "#C4691B"),
]

# --- Связи: (откуда, куда, вид, условие, параметры) ---------------------
# Параметры: t — где на линии подпись (доля от начала), dx/dy — сдвиг,
# path — ломаная для обобщения акторов.
S, O, G = ACT["Staff"], ACT["Owner"], ACT["Guest"]
EDGES = [
    # акторы → основные функции
    ("Guest", "L2", "assoc"), ("Guest", "L8", "assoc"),
    ("Staff", "L1", "assoc"), ("Staff", "L5", "assoc"),
    ("Staff", "L7", "assoc"), ("Staff", "L4", "assoc"),
    ("Owner", "L3", "assoc"), ("Owner", "L6", "assoc"),
    ("Owner", "A1", "assoc"), ("Owner", "A2", "assoc"),
    ("Owner", "E1", "assoc"), ("Owner", "E2", "assoc"),
    ("Cron", "A3", "assoc"), ("Cron", "E2", "assoc"),
    ("e2", "TG", "assoc"), ("e3", "TG", "assoc"), ("e4", "SMTP", "assoc"),
    # обобщение: владелец умеет всё, что сотрудник; посетитель демо — всё, что владелец
    ("Owner", "Staff", "gen", None, dict(path=[(S["x"], O["y"] - 52), (S["x"], S["y"] + 66)])),
    ("Guest", "Owner", "gen", None, dict(path=[
        (G["x"] - 22, G["y"] - 17), (22, G["y"] - 17), (22, O["y"] - 17), (O["x"] - 22, O["y"] - 17)])),
    # локальный функционал
    ("L2", "l2", "include"),
    ("L1", "l1", "include"),
    ("l3", "L3", "extend"),
    ("L5", "l8", "include"),
    ("L7", "l10", "include"),
    ("l11", "L7", "extend", "[факт ≠ заказ > 5%]", dict(dy=36)),
    ("L4", "l4", "include", None, dict(t=0.62)),
    ("l6", "L4", "extend", "[тип = списание]"),
    ("l7", "L4", "extend"),
    ("L4", "l5", "include", None, dict(t=0.62)),
    ("L6", "l9", "include"),
    # мосты из локального функционала в собственное API
    ("L4", "a6", "include", None, dict(t=0.42)),
    ("L6", "a5", "include", None, dict(t=0.86)),
    # собственное API
    ("A1", "a6", "include"),
    ("a6", "a5", "include"),
    ("a5", "a4", "include"),
    ("a5", "a2", "include", None, dict(dx=34, dy=9)),
    ("A2", "a7", "include"),
    ("A2", "a2", "include", None, dict(t=0.62)),
    ("A3", "a2", "include", None, dict(t=0.62)),
    ("a2", "a1", "include"),
    ("a2", "a3", "include"),
    # внешние API
    ("E1", "e1", "include"),
    ("E1", "e2", "include"),
    ("E2", "n1", "include"),
    ("e3", "n1", "extend", "[канал = Telegram]"),
    ("e4", "n1", "extend", "[канал = email]"),
    ("e5", "n1", "extend", "[временная ошибка]", dict(dx=-74, dy=12)),
]


def cond(e):
    return e[3] if len(e) > 3 else None


def opts(e):
    return e[4] if len(e) > 4 and e[4] else {}


# --- Геометрия ----------------------------------------------------------
def node(key):
    if key in UC:
        n = UC[key]
        return n["x"], n["y"], n["rx"], n["ry"]
    a = ACT[key]
    return a["x"], a["y"] - 4, 26, 44


def clip(key, tx, ty):
    """Точка на границе эллипса узла в сторону (tx, ty)."""
    x, y, rx, ry = node(key)
    dx, dy = tx - x, ty - y
    t = 1 / math.sqrt((dx / rx) ** 2 + (dy / ry) ** 2)
    return x + dx * t, y + dy * t


def text_w(s, size, bold=False):
    return len(s) * size * (0.60 if bold else 0.56)


def actor_boxes(key):
    a = ACT[key]
    x, y = a["x"], a["y"]
    boxes = [(x - 22, y - 52, x + 22, y + 32)]
    for i, line in enumerate(a["lines"]):
        w = text_w(line, 13.5, bold=True)
        boxes.append((x - w / 2, y + 38 + i * 16, x + w / 2, y + 54 + i * 16))
    if a.get("stereo"):
        w = text_w("«" + a["stereo"] + "»", 11.5)
        boxes.append((x - w / 2, y - 76, x + w / 2, y - 62))
    return boxes


def in_ellipse(px, py, key, pad):
    x, y, rx, ry = node(key)
    return ((px - x) / (rx + pad)) ** 2 + ((py - y) / (ry + pad)) ** 2 < 1


def in_box(px, py, b, pad=0):
    return b[0] - pad < px < b[2] + pad and b[1] - pad < py < b[3] + pad


def edge_path(e):
    """Ломаная линии связи, уже обрезанная по границам узлов."""
    src, dst, kind = e[0], e[1], e[2]
    if "path" in opts(e):
        return opts(e)["path"]
    if kind == "assoc":
        # Линия входит в крайнюю точку эллипса со стороны актора: к центру
        # она резала бы соседей по колонке. У актора — точка сбоку от
        # фигуры, чтобы линии не перечёркивали его имя.
        actor, case = (src, dst) if src in ACT else (dst, src)
        n, a = UC[case], ACT[actor]
        side = -1 if a["x"] < n["x"] else 1
        anchor = (n["x"] + side * n["rx"], n["y"])
        near = (a["x"] - side * 26, a["y"] - 12)
        return [near, anchor] if src == actor else [anchor, near]
    sx, sy, _, _ = node(src)
    dx, dy, _, _ = node(dst)
    return [clip(src, dx, dy), clip(dst, sx, sy)]


def samples(path, step=3):
    for (x0, y0), (x1, y1) in zip(path, path[1:]):
        n = max(2, int(math.dist((x0, y0), (x1, y1)) / step))
        for i in range(1, n):
            yield x0 + (x1 - x0) * i / n, y0 + (y1 - y0) * i / n


def label_lines(e):
    return ["«" + e[2] + "»"] + ([cond(e)] if cond(e) else [])


def label_anchor(e, path):
    """Центр подписи: точка на доле t линии, чуть над ней."""
    (x0, y0), (x1, y1) = path[0], path[-1]
    o = opts(e)
    t = o.get("t", 0.5)
    return x0 + (x1 - x0) * t + o.get("dx", 0), y0 + (y1 - y0) * t - 9 + o.get("dy", 0)


LABEL_SIZE = 11.5
LABEL_LH = LABEL_SIZE + 3


def label_box(e, path):
    lines = label_lines(e)
    cx, cy = label_anchor(e, path)
    top = cy - (len(lines) - 1) * LABEL_LH
    w = max(text_w(s, LABEL_SIZE) for s in lines)
    return (cx - w / 2, top - LABEL_SIZE * 0.8, cx + w / 2, top + (len(lines) - 1) * LABEL_LH + 3)


# --- Проверка -----------------------------------------------------------
problems = []
paths = {i: edge_path(e) for i, e in enumerate(EDGES)}
labels = {i: label_box(e, paths[i]) for i, e in enumerate(EDGES) if e[2] in ("include", "extend")}

for i, e in enumerate(EDGES):
    src, dst = e[0], e[1]
    for px, py in samples(paths[i]):
        hit = next((k for k in UC if k not in (src, dst) and in_ellipse(px, py, k, 4)), None)
        if hit:
            problems.append(f"линия {src}→{dst} задевает {hit}")
        hit = next((k for k in ACT if k not in (src, dst)
                    and any(in_box(px, py, b, 3) for b in actor_boxes(k))), None)
        if hit:
            problems.append(f"линия {src}→{dst} задевает актора {hit}")
        if e[2] == "assoc" or e[2] == "gen":
            # имя собственного актора линия тоже не должна перечёркивать
            for k in (src, dst):
                if k in ACT and any(in_box(px, py, b, 2) for b in actor_boxes(k)[1:]):
                    problems.append(f"линия {src}→{dst} перечёркивает имя {k}")

for i, b in labels.items():
    e = EDGES[i]
    name = f"{e[0]}→{e[1]}"
    pts = [(b[0], b[1]), (b[2], b[1]), (b[0], b[3]), (b[2], b[3]),
           ((b[0] + b[2]) / 2, b[1]), ((b[0] + b[2]) / 2, b[3])]
    for k in UC:
        if any(in_ellipse(px, py, k, 1) for px, py in pts):
            problems.append(f"подпись {name} наезжает на {k}")
    for j, ob in labels.items():
        if j > i and not (b[2] < ob[0] or ob[2] < b[0] or b[3] < ob[1] or ob[3] < b[1]):
            problems.append(f"подпись {name} наезжает на подпись {EDGES[j][0]}→{EDGES[j][1]}")
    for j, p in paths.items():
        if j != i and any(in_box(px, py, b, 1) for px, py in samples(p)):
            problems.append(f"подпись {name} перечёркнута линией {EDGES[j][0]}→{EDGES[j][1]}")

keys = list(UC)
for i, a in enumerate(keys):
    for b in keys[i + 1:]:
        P, Q = UC[a], UC[b]
        if abs(P["x"] - Q["x"]) < P["rx"] + Q["rx"] + 6 and abs(P["y"] - Q["y"]) < P["ry"] + Q["ry"] + 6:
            problems.append(f"эллипсы {a} и {b} слишком близко")

# --- Рисование ----------------------------------------------------------
out = []
o = out.append
o(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" font-family="{FONT}">')
o(f"""<defs>
<marker id="open" viewBox="0 0 12 12" refX="11" refY="6" markerWidth="12" markerHeight="12" orient="auto" markerUnits="userSpaceOnUse">
<path d="M1,1 L11,6 L1,11" fill="none" stroke="{LINE}" stroke-width="1.4"/></marker>
<marker id="tri" viewBox="0 0 16 16" refX="15" refY="8" markerWidth="16" markerHeight="16" orient="auto" markerUnits="userSpaceOnUse">
<path d="M1,1 L15,8 L1,15 Z" fill="#fff" stroke="{LINE}" stroke-width="1.4"/></marker>
</defs>""")
o(f'<rect width="{W}" height="{H}" fill="#fff"/>')

x0, y0, x1, y1 = SYS
o(f'<rect x="{x0}" y="{y0}" width="{x1 - x0}" height="{y1 - y0}" fill="#fff" stroke="{INK}" stroke-width="1.6" rx="4"/>')
o(f'<text x="{(x0 + x1) / 2}" y="{y0 + 26}" text-anchor="middle" font-size="18" font-weight="700" fill="{INK}">Система Zapas</text>')

for gx0, gy0, gx1, gy1, title, fill, stroke in GROUPS:
    o(f'<rect x="{gx0}" y="{gy0}" width="{gx1 - gx0}" height="{gy1 - gy0}" fill="{fill}" stroke="{stroke}" stroke-width="1.3" rx="6"/>')
    o(f'<text x="{gx1 - 14}" y="{gy0 + 22}" text-anchor="end" font-size="14.5" font-weight="700" fill="{stroke}">{escape(title)}</text>')

for i, e in enumerate(EDGES):
    kind = e[2]
    d = "M" + " L".join(f"{x:.1f},{y:.1f}" for x, y in paths[i])
    if kind == "assoc":
        o(f'<path d="{d}" fill="none" stroke="{LINE}" stroke-width="1.3"/>')
    elif kind == "gen":
        o(f'<path d="{d}" fill="none" stroke="{LINE}" stroke-width="1.3" marker-end="url(#tri)"/>')
    else:
        o(f'<path d="{d}" fill="none" stroke="{LINE}" stroke-width="1.2" stroke-dasharray="6 4" marker-end="url(#open)"/>')

for i, e in enumerate(EDGES):
    if i not in labels:
        continue
    cx, cy = label_anchor(e, paths[i])
    lines = label_lines(e)
    top = cy - (len(lines) - 1) * LABEL_LH
    for j, s in enumerate(lines):
        o(f'<text x="{cx:.1f}" y="{top + j * LABEL_LH:.1f}" text-anchor="middle" font-size="{LABEL_SIZE}" '
          f'font-style="italic" fill="{MUTED}" stroke="#fff" stroke-width="4" stroke-linejoin="round" '
          f'paint-order="stroke">{escape(s)}</text>')

for k, n in UC.items():
    fill = "#FFFFFF" if n["main"] else "#EEF0F3"
    sw = 2.3 if n["main"] else 1.1
    o(f'<ellipse cx="{n["x"]}" cy="{n["y"]}" rx="{n["rx"]}" ry="{n["ry"]}" fill="{fill}" stroke="{INK}" stroke-width="{sw}"/>')
    size = 13.5 if n["main"] else 12.5
    lh = size + 3
    top = n["y"] - (len(n["lines"]) - 1) * lh / 2 + size * 0.36
    for i, s in enumerate(n["lines"]):
        if s.startswith("GET "):
            o(f'<text x="{n["x"]}" y="{top + i * lh:.1f}" text-anchor="middle" font-size="12" font-weight="600" '
              f'fill="#1D4ED8" font-family="Consolas, monospace">{escape(s)}</text>')
        else:
            o(f'<text x="{n["x"]}" y="{top + i * lh:.1f}" text-anchor="middle" font-size="{size}" '
              f'font-weight="{700 if n["main"] else 400}" fill="{INK}">{escape(s)}</text>')

for k, a in ACT.items():
    x, y = a["x"], a["y"]
    o(f'<g fill="none" stroke="{INK}" stroke-width="1.6">'
      f'<circle cx="{x}" cy="{y - 40}" r="11" fill="#fff"/>'
      f'<path d="M{x},{y - 29} V{y + 4} M{x - 20},{y - 17} H{x + 20} M{x},{y + 4} L{x - 15},{y + 30} M{x},{y + 4} L{x + 15},{y + 30}"/></g>')
    if a.get("stereo"):
        o(f'<text x="{x}" y="{y - 64}" text-anchor="middle" font-size="11.5" font-style="italic" fill="{MUTED}">«{escape(a["stereo"])}»</text>')
    for i, s in enumerate(a["lines"]):
        o(f'<text x="{x}" y="{y + 50 + i * 16}" text-anchor="middle" font-size="13.5" font-weight="600" fill="{INK}">{escape(s)}</text>')

# Легенда
mains = sum(1 for n in UC.values() if n["main"])
ly = SYS[3] + 22
o(f'<g font-size="13" fill="{INK}">')
o(f'<ellipse cx="300" cy="{ly + 14}" rx="44" ry="13" fill="#fff" stroke="{INK}" stroke-width="2.3"/>')
o(f'<text x="354" y="{ly + 19}">основная функция — её вызывает актор ({mains})</text>')
o(f'<ellipse cx="300" cy="{ly + 46}" rx="44" ry="13" fill="#EEF0F3" stroke="{INK}" stroke-width="1.1"/>')
o(f'<text x="354" y="{ly + 51}">внутреннее действие ({len(UC) - mains})</text>')
o(f'<path d="M700,{ly + 14} H790" stroke="{LINE}" stroke-width="1.2" stroke-dasharray="6 4" marker-end="url(#open)"/>')
o(f'<text x="802" y="{ly + 19}">«include» — выполняется всегда; «extend» — только при условии в [скобках]</text>')
o(f'<rect x="700" y="{ly + 38}" width="26" height="16" fill="#E8F0FE" stroke="#3B6FD4"/>')
o(f'<text x="734" y="{ly + 51}">собственное API</text>')
o(f'<rect x="870" y="{ly + 38}" width="26" height="16" fill="#FDF0E4" stroke="#C4691B"/>')
o(f'<text x="904" y="{ly + 51}">внешние API</text>')
o(f'<text x="1020" y="{ly + 51}" fill="{MUTED}">Посетитель демо наследует права владельца, но внешние каналы в демо отключены (403).</text>')
o("</g>")
o("</svg>")

OUT.write_text("\n".join(out), encoding="utf-8")

print(f"основных функций: {mains}, внутренних действий: {len(UC) - mains}, "
      f"include: {sum(e[2] == 'include' for e in EDGES)}, extend: {sum(e[2] == 'extend' for e in EDGES)}")
if problems:
    print("ПРОБЛЕМЫ РАСКЛАДКИ:")
    for p in dict.fromkeys(problems):
        print(" -", p)
    sys.exit(1)
print("раскладка чистая:", OUT.name)
