#!/usr/bin/env python3
"""Validate values.yaml @param annotations like the Bitnami readme-generator:
every leaf key must have a @param, and every @param must match a key. A
@param with an [object]/[array] hint covers its whole subtree."""
import re
import sys

import yaml

VALUES = sys.argv[1] if len(sys.argv) > 1 else "charts/codx/values.yaml"

with open(VALUES) as f:
    raw = f.read()
values = yaml.safe_load(raw)

params = set()
hinted = set()
for m in re.finditer(r"^\s*##\s*@param\s+(\S+)(\s+\[(object|array)\])?", raw, re.M):
    params.add(m.group(1))
    if m.group(3):
        hinted.add(m.group(1))

leaves = set()


def flatten(prefix, node):
    if prefix in hinted:
        # A hinted @param documents the whole subtree.
        leaves.add(prefix)
        return
    if isinstance(node, dict):
        if len(node) == 0:
            leaves.add(prefix)
        else:
            for k, v in node.items():
                flatten(f"{prefix}.{k}" if prefix else k, v)
    else:
        # scalars and lists (of any shape) are leaves
        leaves.add(prefix)


flatten("", values)

missing_meta = sorted(k for k in leaves if k not in params)
stale_meta = sorted(k for k in params if k not in leaves)

for k in missing_meta:
    print(f"ERROR: Missing metadata for key: {k}")
for k in stale_meta:
    print(f"ERROR: Metadata provided for non existing key: {k}")

if not missing_meta and not stale_meta:
    print("OK: values metadata consistent")
    sys.exit(0)
sys.exit(1)
