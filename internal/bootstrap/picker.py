#!/usr/bin/env python3
"""Tabbed checklist for devbox-setup (curses, stdlib only).

Reads items from a file and writes the chosen ids, space-separated, to --out.
Exit code 0 = confirmed, 1 = quit.

Input lines:
  T|tab-id|Tab title
  I|tab-id|item-id|label|source       (source: how it gets installed)

--mise FILE adds mise's registry (name<TAB>aliases<TAB>description per line,
aliases comma-separated; `picker.py --registry-tsv < mise-registry.json`
makes it). Those tools appear only in search results; their id is
"mise:<name>".

Keys: typing searches every tab (and the mise tools, which appear only in
search results) · esc clears the search, or quits when it is empty ·
←/→ or tab switch categories · ↑/↓ move · space toggle · enter next tab /
review. Only space changes a selection.
"""
import argparse
import curses
import json
import re
import sys
import textwrap
import unicodedata

REVIEW = "__review__"
SEARCH = "__search__"
MISE = "__mise__"
MAX_RESULTS = 200


def narrow(text):
    """text without double-width characters (emoji, CJK) and :emoji:
    shortcodes, which break the column layout."""
    text = re.sub(r":[a-z0-9_+-]+:", "", text)
    text = "".join(c for c in text if unicodedata.east_asian_width(c) not in "WF"
                   and not unicodedata.combining(c))
    return " ".join(text.split())


def first_sentence(text):
    m = re.match(r"(.+?[.!?])(\s|$)", text)
    return (m.group(1) if m else text).rstrip(".")


def load(path):
    tabs, items = [], []
    with open(path, encoding="utf-8") as f:
        for line in f:
            parts = line.rstrip("\n").split("|")
            if parts[0] == "T" and len(parts) >= 3:
                tabs.append({"id": parts[1], "title": parts[2]})
            elif parts[0] == "I" and len(parts) >= 5:
                items.append({"tab": parts[1], "id": parts[2], "label": parts[3],
                              "source": parts[4], "on": False})
    return tabs, items


def load_mise(path):
    items = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            parts = line.rstrip("\n").split("\t")
            if len(parts) < 3 or not parts[0]:
                continue
            name, desc = parts[0], parts[2]
            aliases = [a for a in parts[1].split(",") if a]
            summary = narrow(first_sentence(desc))
            items.append({"tab": MISE, "id": "mise:" + name,
                          "label": f"{name} — {summary}" if summary else name,
                          "source": "mise use -g " + name, "on": False,
                          "names": [name] + aliases, "text": desc})
    return items


def registry_tsv(data):
    """mise registry -J output → the --mise TSV."""
    clean = lambda s: " ".join(str(s or "").split())
    return "".join(f"{clean(t.get('short'))}\t{','.join(clean(a) for a in t.get('aliases') or [])}"
                   f"\t{clean(t.get('description'))}\n"
                   for t in data if clean(t.get("short")))


def tool_name(spec):
    """'pipx:aider-chat[x]@latest' → 'aider-chat'."""
    spec = spec.split("@")[0].split("[")[0]
    return spec.rsplit(":", 1)[-1].rsplit("/", 1)[-1].lower()


def mise_tools(source):
    """The tools an item's source installs with mise ("mise use -g a b"
    or "mise pipx:x", between "·" separators)."""
    out = set()
    for seg in source.split("·"):
        words = seg.split()
        if words[:1] != ["mise"]:
            continue
        words = words[3:] if words[1:3] == ["use", "-g"] else words[1:]
        out.update(tool_name(w) for w in words)
    return out


def dedupe(items):
    """Drop mise registry rows for tools a catalog item already installs."""
    covered = set()
    for it in items:
        if it["tab"] != MISE:
            covered |= mise_tools(it["source"])
    return [it for it in items
            if it["tab"] != MISE or not covered & {n.lower() for n in it["names"]}]


def fuzzy(q, s, subsequence=True):
    """Score q as a substring, else (when allowed) an in-order subsequence,
    of s (both lower case); None when it does not match."""
    i = s.find(q)
    if i >= 0:
        return (300 if i == 0 or not s[i - 1].isalnum() else 200) - min(len(s), 200) // 20
    if not subsequence:
        return None
    pos, prev, score = 0, -2, 0
    for ch in q:
        j = s.find(ch, pos)
        if j < 0:
            return None
        if j == prev + 1:
            score += 5
        if j == 0 or not s[j - 1].isalnum():
            score += 8
        prev, pos = j, j + 1
    return score - min(len(s), 200) // 20


def names_of(it):
    """(primary, secondary) names a query can match: a mise tool's name,
    then its aliases; a catalog item's id, then its label words and the
    tools it installs."""
    if "names" in it:
        return [n.lower() for n in it["names"][:1]], [n.lower() for n in it["names"][1:]]
    words = re.findall(r"[\w.+-]+", it["label"].lower())
    return [it["id"].lower()], words + sorted(mise_tools(it["source"]))


def name_score(q, n):
    if n == q:
        return 1000
    if n.startswith(q):
        return 900 - len(n)
    if q in n:
        return 600 - len(n)
    return None


def score(it, q):
    """Higher is better; None = no match. Primary names first (exact,
    prefix, substring), then secondary names, then a fuzzy match on the
    label (a mise tool's names), then the source or description as a
    substring. Catalog items rank ahead of mise tools."""
    q = q.lower()
    primary, secondary = names_of(it)
    cands = [name_score(q, n) for n in primary]
    cands += [s - 150 for s in (name_score(q, n) for n in secondary) if s is not None]
    if it["tab"] == MISE:  # the label holds the description: names only
        cands += [fuzzy(q, n) for n in primary + secondary]
    else:
        cands.append(fuzzy(q, it["label"].lower()))
    s = fuzzy(q, it.get("text", it["source"]).lower(), subsequence=False)
    cands.append(None if s is None else s - 50)
    cands = [c for c in cands if c is not None]
    if not cands:
        return None
    return max(cands) + (0 if it["tab"] == MISE else 200)


def search(items, q, limit=MAX_RESULTS):
    scored = [(score(it, q), n, it) for n, it in enumerate(items)]
    scored = [x for x in scored if x[0] is not None]
    scored.sort(key=lambda x: (-x[0], x[2]["tab"] == MISE, len(x[2]["label"]), x[1]))
    return [it for _, _, it in scored[:limit]]


class Picker:
    def __init__(self, scr, title, tabs, items, review):
        self.scr, self.title, self.items = scr, title, items
        self.tabs = [t for t in tabs if any(i["tab"] == t["id"] for i in items)]
        self.review = review
        if review:
            self.tabs.append({"id": REVIEW, "title": "Review"})
        self.tab_title = {t["id"]: t["title"] for t in self.tabs}
        self.tab_title[MISE] = "mise"
        self.tab = 0
        self.query, self.results = "", []
        self.cursor = {t["id"]: 0 for t in self.tabs}
        self.top = {t["id"]: 0 for t in self.tabs}
        self.cursor[SEARCH] = self.top[SEARCH] = 0
        curses.curs_set(0)
        curses.use_default_colors()
        self.has_color = curses.has_colors()
        if self.has_color:
            curses.init_pair(1, 212, -1) if curses.COLORS >= 256 else curses.init_pair(1, curses.COLOR_MAGENTA, -1)
            curses.init_pair(2, curses.COLOR_GREEN, -1)
            curses.init_pair(3, 244 if curses.COLORS >= 256 else curses.COLOR_WHITE, -1)
            curses.init_pair(4, curses.COLOR_BLACK, 212 if curses.COLORS >= 256 else curses.COLOR_MAGENTA)

    def c(self, n, extra=0):
        return (curses.color_pair(n) if self.has_color else 0) | extra

    def view(self):
        """The list being shown: search results, or the current tab."""
        return SEARCH if self.query else self.tabs[self.tab]["id"]

    def tab_items(self, tab_id=None):
        tab_id = tab_id or self.view()
        if tab_id == SEARCH:
            return self.results
        if tab_id == REVIEW:
            return [i for i in self.items if i["on"]]
        return [i for i in self.items if i["tab"] == tab_id]

    def set_query(self, q):
        self.query = q
        self.results = search(self.items, q) if q else []
        self.cursor[SEARCH] = self.top[SEARCH] = 0

    def put(self, y, x, text, attr=0):
        h, w = self.scr.getmaxyx()
        if 0 <= y < h and x < w:
            try:
                self.scr.addnstr(y, x, text, max(0, w - x - 1), attr)
            except curses.error:
                pass

    def draw_tabs(self, w, chosen):
        # Tab bar: scrolls so the current tab is always visible.
        names = []
        for t in self.tabs:
            k = sum(i["on"] for i in self.tab_items(t["id"])) if t["id"] != REVIEW else chosen
            names.append(f" {t['title']}" + (f" ({k})" if k else "") + " ")
        first = 0
        while sum(len(n) + 1 for n in names[first:self.tab + 1]) > w - 6 and first < self.tab:
            first += 1
        x = 1
        if first > 0:
            self.put(2, x, "‹", self.c(3))
            x += 2
        for n in range(first, len(names)):
            if x + len(names[n]) >= w - 2:
                self.put(2, w - 3, "›", self.c(3))
                break
            attr = self.c(4, curses.A_BOLD) if n == self.tab else self.c(3)
            self.put(2, x, names[n], attr)
            x += len(names[n]) + 1

    def draw(self):
        scr = self.scr
        scr.erase()
        h, w = scr.getmaxyx()
        chosen = sum(i["on"] for i in self.items)
        self.put(0, 1, self.title, self.c(1, curses.A_BOLD))
        right = f"{chosen} selected"
        self.put(0, max(1, w - len(right) - 2), right, self.c(2, curses.A_BOLD) if chosen else self.c(3))

        view = self.view()
        if view == SEARCH:
            self.put(2, 1, "Search: ", self.c(3))
            self.put(2, 9, self.query + "_", self.c(1, curses.A_BOLD))
            n = len(self.results)
            count = f"{n}{'+' if n >= MAX_RESULTS else ''} match{'' if n == 1 else 'es'}"
            self.put(2, max(10 + len(self.query) + 2, w - len(count) - 2), count, self.c(3))
        else:
            self.draw_tabs(w, chosen)
        self.put(3, 0, "─" * (w - 1), self.c(3))

        items = self.tab_items()
        list_top, list_h = 4, max(1, h - 4 - 2)
        cur = min(self.cursor[view], max(0, len(items) - 1))
        self.cursor[view] = cur

        # Rows wrap instead of being cut: the label and the source each wrap
        # within their column, and a row is as tall as the longer of the two.
        # Search results show the category instead of the source, and mise
        # tools take one line.
        if view == SEARCH:
            src_w = min(14, max(len(self.tab_title.get(i["tab"], "")) for i in items)) if items else 4
            label_w = max(14, w - 7 - src_w - 3)
        else:
            label_w = min(42, max([len(i["label"]) for i in items] + [10]) + 2, max(14, (w - 7) // 2))
        src_x = 7 + label_w
        src_w = max(4, w - src_x - 3)
        rows = []
        for it in items:
            if it["tab"] == MISE:
                lab = [textwrap.shorten(it["label"], label_w - 2, placeholder="…")]
            else:
                lab = textwrap.wrap(it["label"], label_w - 2, break_on_hyphens=False) or [""]
            right = self.tab_title.get(it["tab"], "") if view == SEARCH else it["source"]
            src = textwrap.wrap(right, src_w, break_on_hyphens=False) or [""]
            rows.append((lab, src, max(len(lab), len(src))))

        top = self.top[view]
        if cur < top:
            top = cur
        while top < cur and sum(r[2] for r in rows[top:cur + 1]) > list_h:
            top += 1
        self.top[view] = top

        if not items:
            if view == SEARCH:
                msg = "No matches — backspace to edit, esc to clear."
            elif view == REVIEW:
                msg = "Nothing selected yet — go back with ← and tick items with space."
            else:
                msg = "(empty)"
            self.put(list_top + 1, 3, msg, self.c(3))
        y, last = list_top, top - 1
        for idx in range(top, len(items)):
            if y >= list_top + list_h:
                break
            it = items[idx]
            lab, src, rh = rows[idx]
            sel = idx == cur
            self.put(y, 1, "›" if sel else " ", self.c(1, curses.A_BOLD))
            self.put(y, 3, "[x]" if it["on"] else "[ ]", self.c(2, curses.A_BOLD) if it["on"] else self.c(3))
            for n in range(rh):
                if y + n >= list_top + list_h:
                    break
                if n < len(lab):
                    self.put(y + n, 7, lab[n], curses.A_BOLD if sel else 0)
                if n < len(src):
                    self.put(y + n, src_x, src[n], self.c(3))
            if y + rh <= list_top + list_h:
                last = idx
            y += rh
        if top > 0:
            self.put(list_top, w - 2, "↑", self.c(3))
        if last < len(items) - 1:
            self.put(list_top + list_h - 1, w - 2, "↓", self.c(3))

        if view == SEARCH:
            keys = "esc clear · ↑/↓ move · space toggle · enter " + ("review" if self.review else "confirm")
        elif view == REVIEW:
            keys = "space untick · ← back · type to search · enter confirm · esc quit"
        else:
            keys = "type to search · ←/→ category · ↑/↓ move · space toggle · enter next · esc quit"
        self.put(h - 1, 1, keys, self.c(3))
        scr.refresh()

    def run(self):
        while True:
            self.draw()
            k = self.scr.getch()
            view = self.view()
            items = self.tab_items()
            cur = self.cursor[view]
            # Only space changes a selection: typing searches, never ticks.
            if k == curses.KEY_DOWN:
                self.cursor[view] = min(cur + 1, max(0, len(items) - 1))
            elif k == curses.KEY_UP:
                self.cursor[view] = max(cur - 1, 0)
            elif k == curses.KEY_HOME:
                self.cursor[view] = 0
            elif k == curses.KEY_END:
                self.cursor[view] = max(0, len(items) - 1)
            elif k == curses.KEY_NPAGE:
                self.cursor[view] = min(cur + 10, max(0, len(items) - 1))
            elif k == curses.KEY_PPAGE:
                self.cursor[view] = max(cur - 10, 0)
            elif k == ord(" ") and items:
                items[cur]["on"] = not items[cur]["on"]
                if view != REVIEW:
                    self.cursor[view] = min(cur + 1, len(items) - 1)
            elif k in (curses.KEY_BACKSPACE, 127, 8):
                if self.query:
                    self.set_query(self.query[:-1])
            elif k == 27:
                if not self.query:
                    return False
                self.set_query("")
            elif k in (curses.KEY_ENTER, 10, 13):
                if self.query:
                    self.set_query("")
                    if not self.review:
                        return True
                    self.tab = len(self.tabs) - 1
                elif self.tab == len(self.tabs) - 1:
                    return True
                else:
                    self.tab += 1
            elif self.query:
                if 33 <= k <= 126:
                    self.set_query(self.query + chr(k))
            elif k in (curses.KEY_RIGHT, ord("\t")):
                self.tab = (self.tab + 1) % len(self.tabs)
            elif k in (curses.KEY_LEFT, curses.KEY_BTAB):
                self.tab = (self.tab - 1) % len(self.tabs)
            elif ord("1") <= k <= ord("9") and k - ord("1") < len(self.tabs):
                self.tab = k - ord("1")
            elif 33 <= k <= 126:
                self.set_query(chr(k))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("items", nargs="?")
    ap.add_argument("--out")
    ap.add_argument("--mise", help="mise registry TSV: searchable extra tools")
    ap.add_argument("--registry-tsv", action="store_true",
                    help="convert `mise registry -J` on stdin to the --mise TSV and exit")
    ap.add_argument("--title", default="devbox-setup")
    ap.add_argument("--no-review", action="store_true", help="single list: enter confirms")
    a = ap.parse_args()
    if a.registry_tsv:
        sys.stdout.write(registry_tsv(json.load(sys.stdin)))
        return
    if not a.items or not a.out:
        ap.error("items and --out are required")
    tabs, items = load(a.items)
    if a.mise:
        try:
            items = dedupe(items + load_mise(a.mise))
        except OSError:
            pass
    try:
        ok = curses.wrapper(lambda scr: Picker(scr, a.title, tabs, items, not a.no_review).run())
    except KeyboardInterrupt:
        ok = False
    with open(a.out, "w", encoding="utf-8") as f:
        f.write(" ".join(i["id"] for i in items if i["on"]))
    raise SystemExit(0 if ok else 1)


if __name__ == "__main__":
    if hasattr(curses, "set_escdelay"):
        curses.set_escdelay(25)
    main()
