#!/usr/bin/env python3
"""Compatibility entrypoint for P6-B3 pull-secret-kubernetes track."""

from __future__ import annotations

import sys

from run_registry_harbor_live_gate import main


if __name__ == "__main__":
    argv = ["--track", "pull-secret-kubernetes", *sys.argv[1:]]
    raise SystemExit(main(argv))
