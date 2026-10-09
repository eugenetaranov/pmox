#!/usr/bin/env python3
"""Tabbed checklist for devbox-setup (curses, stdlib only).

Reads items from a file and writes the chosen ids, space-separated, to --out.
Exit code 0 = confirmed, 1 = quit.

Input lines:
  T|tab-id|Tab title
  I|tab-id|item-id|label|source       (source: how it gets installed)

Keys: ←/→ or tab switch categories · ↑/↓ move · space toggle ·
      enter next tab / review · q quit. Only space changes a selection.
"""
import argparse
import curses
import textwrap

REVIEW = "__review__"


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


class Picker:
    def __init__(self, scr, title, tabs, items, review):
        self.scr, self.title, self.items = scr, title, items
        self.tabs = [t for t in tabs if any(i["tab"] == t["id"] for i in items)]
        if review:
            self.tabs.append({"id": REVIEW, "title": "Review"})
        self.tab = 0
        self.cursor = {t["id"]: 0 for t in self.tabs}
        self.top = {t["id"]: 0 for t in self.tabs}
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

    def tab_items(self, tab_id=None):
        tab_id = tab_id or self.tabs[self.tab]["id"]
        if tab_id == REVIEW:
            return [i for i in self.items if i["on"]]
        return [i for i in self.items if i["tab"] == tab_id]

    def put(self, y, x, text, attr=0):
        h, w = self.scr.getmaxyx()
        if 0 <= y < h and x < w:
            try:
                self.scr.addnstr(y, x, text, max(0, w - x - 1), attr)
            except curses.error:
                pass

    def draw(self):
        scr = self.scr
        scr.erase()
        h, w = scr.getmaxyx()
        chosen = sum(i["on"] for i in self.items)
        self.put(0, 1, self.title, self.c(1, curses.A_BOLD))
        right = f"{chosen} selected"
        self.put(0, max(1, w - len(right) - 2), right, self.c(2, curses.A_BOLD) if chosen else self.c(3))

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
        self.put(3, 0, "─" * (w - 1), self.c(3))

        tab_id = self.tabs[self.tab]["id"]
        items = self.tab_items()
        list_top, list_h = 4, max(1, h - 4 - 2)
        cur = min(self.cursor[tab_id], max(0, len(items) - 1))
        self.cursor[tab_id] = cur

        # Rows wrap instead of being cut: the label and the source each wrap
        # within their column, and a row is as tall as the longer of the two.
        label_w = min(42, max([len(i["label"]) for i in items] + [10]) + 2, max(14, (w - 7) // 2))
        src_x = 7 + label_w
        src_w = max(10, w - src_x - 3)
        rows = []
        for it in items:
            lab = textwrap.wrap(it["label"], label_w - 2, break_on_hyphens=False) or [""]
            src = textwrap.wrap(it["source"], src_w, break_on_hyphens=False) or [""]
            rows.append((lab, src, max(len(lab), len(src))))

        top = self.top[tab_id]
        if cur < top:
            top = cur
        while top < cur and sum(r[2] for r in rows[top:cur + 1]) > list_h:
            top += 1
        self.top[tab_id] = top

        if not items:
            msg = "Nothing selected yet — go back with ← and tick items with space." if tab_id == REVIEW else "(empty)"
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

        if tab_id == REVIEW:
            keys = "space untick · ← back · enter confirm · q quit"
        else:
            keys = "←/→ category · ↑/↓ move · space toggle · enter next · q quit"
        self.put(h - 1, 1, keys, self.c(3))
        scr.refresh()

    def run(self):
        while True:
            self.draw()
            k = self.scr.getch()
            tab_id = self.tabs[self.tab]["id"]
            items = self.tab_items()
            cur = self.cursor[tab_id]
            # Only space changes a selection: stray letters must never tick things.
            if k in (curses.KEY_RIGHT, ord("\t")):
                self.tab = (self.tab + 1) % len(self.tabs)
            elif k in (curses.KEY_LEFT, curses.KEY_BTAB):
                self.tab = (self.tab - 1) % len(self.tabs)
            elif k == curses.KEY_DOWN:
                self.cursor[tab_id] = min(cur + 1, max(0, len(items) - 1))
            elif k == curses.KEY_UP:
                self.cursor[tab_id] = max(cur - 1, 0)
            elif k == curses.KEY_HOME:
                self.cursor[tab_id] = 0
            elif k == curses.KEY_END:
                self.cursor[tab_id] = max(0, len(items) - 1)
            elif k == ord(" ") and items:
                items[cur]["on"] = not items[cur]["on"]
                if tab_id != REVIEW:
                    self.cursor[tab_id] = min(cur + 1, len(items) - 1)
            elif ord("1") <= k <= ord("9") and k - ord("1") < len(self.tabs):
                self.tab = k - ord("1")
            elif k in (curses.KEY_ENTER, 10, 13):
                if self.tab == len(self.tabs) - 1:
                    return True
                self.tab += 1
            elif k in (ord("q"), 27):
                return False
            elif k == curses.KEY_RESIZE:
                pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("items")
    ap.add_argument("--out", required=True)
    ap.add_argument("--title", default="devbox-setup")
    ap.add_argument("--no-review", action="store_true", help="single list: enter confirms")
    a = ap.parse_args()
    tabs, items = load(a.items)
    ok = curses.wrapper(lambda scr: Picker(scr, a.title, tabs, items, not a.no_review).run())
    with open(a.out, "w", encoding="utf-8") as f:
        f.write(" ".join(i["id"] for i in items if i["on"]))
    raise SystemExit(0 if ok else 1)


if __name__ == "__main__":
    if hasattr(curses, "set_escdelay"):
        curses.set_escdelay(25)
    main()
