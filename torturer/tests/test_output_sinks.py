"""Keep product command diagnostics from acquiring silent output sinks."""

from __future__ import annotations

from pathlib import Path
import re
import unittest


_ROOT = Path(__file__).resolve().parents[2]
_FILES = (
    _ROOT / ".github/scripts",
    _ROOT / ".github/workflows",
    _ROOT / "torturer/torturer_checks",
    _ROOT / "go_module/scripts",
    _ROOT / "go_module/cmd",
    _ROOT / "go_module/ui",
    _ROOT / "android_module/gradlew",
    _ROOT / "android_module/app/src",
    _ROOT / "swift_module",
    _ROOT / "installer",
    _ROOT / ".lefthook.yml",
)

_SINKS = (
    re.compile(r"(?<![<\w])\d?>\s*/dev/null"),
    re.compile(r"(?<![<\w])2>\s*/dev/null"),
    re.compile(r"\b(?:stdout|stderr)\s*=\s*subprocess\.DEVNULL\b"),
    re.compile(r"\bOut-Null\b"),
    re.compile(r"\bSilentlyContinue\b"),
    re.compile(r"(?<![\w-])(?:--quiet|-q)(?![\w-])"),
    re.compile(r"(?<!\S)-print\s+-quit\b"),
)
_QUIET_FLAGS = _SINKS[-2]


def _product_files() -> list[Path]:
    files: list[Path] = []
    excluded_directories = {
        "build", "generated", ".gradle", "__pycache__", "Tests",
    }
    for entry in _FILES:
        if entry.is_file():
            files.append(entry)
        elif entry.is_dir():
            files.extend(
                path for path in entry.rglob("*")
                if path.is_file()
                and not any(part in excluded_directories for part in path.parts)
                and not path.name.startswith("test_")
                and not path.name.endswith("_test.go")
                and path.suffix in {
                    ".go", ".java", ".kt", ".py", ".sh", ".swift",
                    ".yml", ".yaml", ".ps1", ".cmd", ".bat",
                }
            )
    return sorted(set(files))


class ProductOutputSinkTests(unittest.TestCase):
    def test_owned_diagnostic_paths_do_not_suppress_operational_output(self) -> None:
        violations: list[str] = []
        for path in _product_files():
            text = path.read_text(encoding="utf-8")
            for pattern in _SINKS:
                for match in pattern.finditer(text):
                    if pattern is _QUIET_FLAGS:
                        line = text[text.rfind("\n", 0, match.start()) + 1:text.find("\n", match.end())]
                        # dscacheutil's ``-q host`` is a query selector, not
                        # a quiet/suppression flag; keep this one explicit
                        # exception local to its complete command line.
                        if "dscacheutil" in line and '"-q"' in line:
                            continue
                    violations.append(f"{path.relative_to(_ROOT)}: {pattern.pattern}")
        self.assertEqual(
            violations,
            [],
            "Output sinks require a reviewed, forwarding-safe exception. "
            "stdin /dev/null and tee /dev/stderr are intentionally allowed; "
            "the latter forwards the complete captured stream.\n" + "\n".join(violations),
        )


if __name__ == "__main__":
    unittest.main()
