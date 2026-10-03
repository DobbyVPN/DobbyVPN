"""Additional retained app streams, optional until written or rotated."""

from __future__ import annotations

import shlex

OPTIONAL_MISSING = 44


def retained_log_sources():
    directory = "/data/user/0/com.dobby.vpn/files/diagnostics"
    for filename in (
        "native_logs.jsonl.previous",
        "go_app_logs.jsonl.previous",
        "go_app_logs.jsonl.stderr",
        "go_app_logs.jsonl.stderr.previous",
        "ui_diagnostics.jsonl",
        "ui_diagnostics.jsonl.previous",
    ):
        path = shlex.quote(f"{directory}/{filename}")
        # Missing generations are expected. Any failed read and its original
        # stdout/stderr still reach the caller's ordinary collection failure.
        script = f"if [ ! -e {path} ]; then exit {OPTIONAL_MISSING}; fi; cat {path}"
        yield (
            "ANDROID_RETAINED_LOG_COLLECTION_FAILED",
            f"android-{filename}-diagnostics",
            ["shell", "-T", "sh", "-c", shlex.quote(script)],
            filename,
            False,
        )
