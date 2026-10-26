"""Validate resolved Gradle inventories and the packages actually read by Trivy."""
from __future__ import annotations

import json
import re
from collections import defaultdict
from pathlib import Path
from urllib.parse import quote


GROUPS = ("release-runtime", "buildscript", "unit-test", "instrumentation-test", "project")
COORDINATE = re.compile(r"[^\s:=,]+:[^\s:=,]+:[^\s:=,]+\Z")


def scanner_inputs(inventory_path: Path, directory: Path, *, require_tests: bool = False) -> dict[str, set[str]]:
    inventory = json.loads(inventory_path.read_text(encoding="utf-8"))
    if not isinstance(inventory, dict) or inventory.get("schema_version") != 1:
        raise ValueError("unsupported Android dependency inventory schema")
    projects = inventory.get("projects")
    if (not isinstance(projects, list) or any(not isinstance(item, str) for item in projects)
            or len(projects) != len(set(projects)) or not {":", ":app", ":presentation"}.issubset(projects)):
        raise ValueError("Android inventory is missing expected projects")
    configurations = inventory.get("configurations")
    if not isinstance(configurations, list) or not configurations:
        raise ValueError("Android dependency inventory has no configurations")
    groups: dict[str, dict[str, set[str]]] = {name: defaultdict(set) for name in GROUPS}
    seen: set[tuple[str, str, str]] = set()
    for entry in configurations:
        if not isinstance(entry, dict):
            raise ValueError("invalid Android configuration inventory")
        project, kind, name, group = (entry.get(key) for key in ("project", "kind", "configuration", "group"))
        if project not in projects or kind not in ("project", "buildscript") or not isinstance(name, str) or not name:
            raise ValueError(f"invalid Android configuration identity: {entry}")
        identity = (project, kind, name)
        if identity in seen or group not in GROUPS:
            raise ValueError(f"duplicate or unclassified Android configuration: {identity}")
        seen.add(identity)
        lower = name.lower()
        expected_group = ("buildscript" if kind == "buildscript" else "instrumentation-test" if "androidtest" in lower
                          else "unit-test" if "unittest" in lower or lower.startswith("test")
                          else "release-runtime" if project == ":app" and name == "releaseRuntimeClasspath" else "project")
        if group != expected_group:
            raise ValueError(f"wrong Android dependency group for {identity}: {group}")
        modules = entry.get("modules")
        if not isinstance(modules, list) or any(not isinstance(item, str) or not COORDINATE.fullmatch(item) for item in modules):
            raise ValueError(f"invalid resolved module inventory for {identity}")
        for module in modules:
            groups[group][module].add(f"{project}/{kind}/{name}")
    required = {(project, "buildscript", "classpath") for project in projects}
    required.add((":app", "project", "releaseRuntimeClasspath"))
    if require_tests:
        required.update({(":presentation", "project", "testRuntimeClasspath"),
                         (":app", "project", "releaseAndroidTestRuntimeClasspath")})
    if missing := required - seen:
        raise ValueError(f"Android inventory is missing configurations: {sorted(missing)}")
    required_groups = ("release-runtime", "buildscript", "unit-test", "instrumentation-test") if require_tests else ("release-runtime", "buildscript")
    for group in required_groups:
        if not groups[group]:
            raise ValueError(f"Android inventory is unexpectedly empty: {group}")
    build_modules = {item.rsplit(":", 1)[0] for item in groups["buildscript"]}
    if not {"com.android.tools.build:gradle", "org.jetbrains.kotlin:kotlin-gradle-plugin"}.issubset(build_modules):
        raise ValueError("Android plugin classpath inventory is incomplete")
    directory.mkdir(parents=True, exist_ok=True)
    expected: dict[str, set[str]] = {}
    for group, modules in groups.items():
        if not modules:
            continue
        name = f"{group}.gradle.lockfile"
        # This is scanner input from the resolved graph, never a build lock.
        lines = ["# Resolved Gradle security inventory; not a build lockfile."]
        # Trivy's Gradle parser splits the whole line at colons. Encode project
        # paths in the configuration attribution so only the module has colons.
        lines += [f"{module}={','.join(quote(config, safe='/') for config in sorted(configs))}"
                  for module, configs in sorted(modules.items())]
        (directory / name).write_text("\n".join(lines) + "\n", encoding="utf-8")
        expected[name] = set(modules)
    return expected


def verify_scanner_report(report_path: Path, expected: dict[str, set[str]]) -> None:
    report = json.loads(report_path.read_text(encoding="utf-8"))
    if not isinstance(report, dict) or not isinstance(report.get("Results"), list):
        raise ValueError("Trivy did not produce a package report for Android inventories")
    found: dict[str, set[str]] = {}
    for result in report["Results"]:
        name = Path(result.get("Target", "")).name
        if name not in expected:
            continue
        if name in found or result.get("Type") != "gradle":
            raise ValueError(f"Trivy returned an invalid Android inventory result: {name}")
        packages = result.get("Packages")
        if not isinstance(packages, list) or not packages:
            raise ValueError(f"Trivy did not read packages from {name}")
        found[name] = {f"{package.get('Name')}:{package.get('Version')}" for package in packages}
    for name, modules in expected.items():
        if missing := modules - found.get(name, set()):
            raise ValueError(f"Trivy omitted resolved Android packages from {name}: {sorted(missing)}")
