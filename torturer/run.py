#!/usr/bin/env python3
"""Stable local Torturer entry point used by VM runners."""

from torturer_checks.functional import main


if __name__ == "__main__":
    raise SystemExit(main())
