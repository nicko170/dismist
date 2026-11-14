#!/usr/bin/env python3
"""Evaluate every query in the generated dashboards against a Prometheus.

Usage: tools/dashboards/check_queries.py PROMETHEUS_URL DASHBOARD_DIR

Dashboard variables are replaced with values that match the exporter
fixture. A query that Prometheus refuses (a parse error, a many-to-many
join) fails the check; a query with no data is only listed, because the
fixture has no process_* series and no changes for the annotations to find.
Stdlib only.
"""
import glob
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

VARIABLES = {"$namespace": "dismist", "$site": ".*", "$ap": "02-aa-00-00-00-01", "$radio": "r0",
             "$client": ".*", "$__rate_interval": "1m"}


def expressions(node, title="", out=None):
    out = [] if out is None else out
    if isinstance(node, dict):
        title = node.get("title", title)
        if isinstance(node.get("expr"), str):
            out.append((title, node["expr"]))
        for value in node.values():
            expressions(value, title, out)
    elif isinstance(node, list):
        for value in node:
            expressions(value, title, out)
    return out


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__.strip().splitlines()[2])
    base, directory = sys.argv[1].rstrip("/"), sys.argv[2]
    errors, empty, total = [], [], 0
    for path in sorted(glob.glob(os.path.join(directory, "*.json"))):
        with open(path) as f:
            board = json.load(f)
        for title, expr in expressions(board):
            query = expr
            for name, value in VARIABLES.items():
                query = query.replace(name, value)
            total += 1
            url = base + "/api/v1/query?" + urllib.parse.urlencode({"query": query})
            try:
                with urllib.request.urlopen(url, timeout=10) as response:
                    result = json.load(response)["data"]["result"]
                if not result:
                    empty.append(f"{os.path.basename(path)}: {title}: {expr[:120]}")
            except urllib.error.HTTPError as err:
                errors.append(f"{os.path.basename(path)}: {title}: {err.read().decode()[:300]}\n    {expr}")
    for line in empty:
        print("no data:", line)
    for line in errors:
        print("ERROR:", line, file=sys.stderr)
    print(f"{total} queries, {len(errors)} errors, {len(empty)} without fixture data")
    sys.exit(1 if errors else 0)


if __name__ == "__main__":
    main()
