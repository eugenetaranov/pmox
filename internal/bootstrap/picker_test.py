"""Tests for picker.py's search: python3 -m unittest picker_test (run from
this directory; bootstrap_test.go runs it too)."""
import os
import re
import tempfile
import unittest

import picker

HERE = os.path.dirname(os.path.abspath(__file__))


def catalog():
    """The real catalog, as devbox-setup's ask() writes it."""
    with open(os.path.join(HERE, "devbox-setup"), encoding="utf-8") as f:
        src = f.read()
    body = re.search(r"^CATALOG_ROWS=\((.*?)^\)", src, re.S | re.M).group(1)
    items = []
    for row in re.findall(r'^\s*"([^"]+)"', body, re.M):
        f = row.split("|")
        rid, tab, label, how, what = f[0], f[1], f[2], f[3], f[4]
        if how == "mise":
            source = "mise use -g " + what.replace("@latest", "")
        elif how == "apt":
            source = "apt install " + what
        else:
            source = f[6] if len(f) > 6 else ""
        items.append({"tab": tab, "id": rid, "label": label, "source": source, "on": False})
    return items


def everything():
    return picker.dedupe(catalog() + picker.load_mise(os.path.join(HERE, "mise-registry.tsv")))


def ids(results, n):
    return [i["id"] for i in results[:n]]


class SearchTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.items = everything()

    def test_zsh_catalog_first(self):
        top = ids(picker.search(self.items, "zsh"), 2)
        self.assertCountEqual(top, ["shell", "fzftab"])

    def test_catalog_before_mise(self):
        res = picker.search(self.items, "zsh")
        kinds = [i["tab"] == picker.MISE for i in res]
        first_mise = kinds.index(True)
        self.assertTrue(all(kinds[first_mise:first_mise + 1]))
        self.assertIn("shell", ids(res, first_mise))

    def test_tool_inside_catalog_item(self):
        self.assertEqual(ids(picker.search(self.items, "k9s"), 1), ["k8s"])

    def test_description_match(self):
        self.assertIn("mise:fd", ids(picker.search(self.items, "find"), 40))

    def test_mise_name_prefix(self):
        self.assertEqual(ids(picker.search(self.items, "starship"), 1), ["mise:starship"])

    def test_descriptions_not_fuzzy(self):
        self.assertLess(len(picker.search(self.items, "starsh")), 10)

    def test_mise_name_fuzzy(self):
        self.assertIn("mise:starship", ids(picker.search(self.items, "strshp"), 3))

    def test_no_match(self):
        self.assertEqual(picker.search(self.items, "qqqqzzzz"), [])

    def test_fuzzy_subsequence(self):
        self.assertIn("lazygit", ids(picker.search(self.items, "lzgt"), 5))


class NarrowTest(unittest.TestCase):
    def test_wide_chars_dropped(self):
        self.assertEqual(picker.narrow(":cherry_blossom: A fuzzy finder 🚀 for おはよう you"),
                         "A fuzzy finder for you")
        self.assertEqual(picker.narrow("Kubernetes — ⎈ helm…"), "Kubernetes — ⎈ helm…")


class DedupeTest(unittest.TestCase):
    def test_catalog_tools_hidden(self):
        names = {i["id"] for i in everything()}
        for covered in ("terraform", "k9s", "kubectl", "glances", "helmfile", "aider-chat", "neovim"):
            self.assertNotIn("mise:" + covered, names)
        self.assertIn("mise:starship", names)

    def test_mise_tools(self):
        self.assertEqual(picker.mise_tools("mise use -g uv pipx:aider-chat[uvx_args=--python=3.12] aqua:block/goose"),
                         {"uv", "aider-chat", "goose"})
        self.assertEqual(picker.mise_tools("apt prometheus-node-exporter · mise pipx:glances"), {"glances"})
        self.assertEqual(picker.mise_tools("apt install mise"), set())


class LoadTest(unittest.TestCase):
    def test_registry_tsv_roundtrip(self):
        tsv = picker.registry_tsv([
            {"short": "op", "aliases": ["1password"], "description": "Password manager. Second\tsentence."},
            {"short": "", "description": "skipped"},
            {"short": "bare"},
        ])
        with tempfile.NamedTemporaryFile("w", suffix=".tsv", delete=False) as f:
            f.write(tsv)
        try:
            got = picker.load_mise(f.name)
        finally:
            os.unlink(f.name)
        self.assertEqual([i["id"] for i in got], ["mise:op", "mise:bare"])
        self.assertEqual(got[0]["label"], "op — Password manager")
        self.assertEqual(got[0]["names"], ["op", "1password"])
        self.assertEqual(got[0]["source"], "mise use -g op")
        self.assertEqual(got[1]["label"], "bare")


if __name__ == "__main__":
    unittest.main()
