#!/usr/bin/env python3
"""Turns every PromQL query of the Grafana dashboard into a recording rule, so that
`promtool check rules` parses them all (SP-11). A query with a typo would otherwise be found only
by opening the dashboard. Grafana's variables are replaced with values PromQL accepts.

    python3 test/grafana/dashboard_queries.py dist/chart/files/streamprobe-dashboard.json > rules.yaml
    promtool check rules rules.yaml
"""
import json
import sys

VARIABLES = {
    "$__rate_interval": "5m",
    "$__range": "1h",
    "$namespace": ".*",
    "$stream": ".*",
}


def queries(dashboard):
    for panel in dashboard["panels"]:
        for target in panel.get("targets", []):
            yield panel["title"], target["refId"], target["expr"]
    for var in dashboard.get("templating", {}).get("list", []):
        query = var.get("query")
        if isinstance(query, dict) and query.get("query", "").startswith("label_values("):
            # label_values(metric, label) is Grafana's, not PromQL: check its selector.
            inner = query["query"][len("label_values("):].rsplit(",", 1)[0]
            yield "variable " + var["name"], "q", inner


def main():
    dashboard = json.load(open(sys.argv[1]))
    print("groups:\n  - name: dashboard-queries\n    rules:")
    n = 0
    for title, ref, expr in queries(dashboard):
        for k, v in VARIABLES.items():
            expr = expr.replace(k, v)
        n += 1
        print(f"      # {title} ({ref})")
        print(f"      - record: dashboard:query_{n}")
        print("        expr: |")
        for line in expr.splitlines():
            print("          " + line)
    print(f"{n} queries", file=sys.stderr)


if __name__ == "__main__":
    main()
