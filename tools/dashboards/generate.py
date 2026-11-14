#!/usr/bin/env python3
"""Generate the dismist Grafana dashboards shipped in charts/dismist/monitoring/dashboards.

Usage: tools/dashboards/generate.py charts/dismist/monitoring/dashboards

The dashboards only query metrics dismistd emits (internal/metrics checks every
metric name they use). Names come from dismist_site_info / dismist_ap_info,
which carry a `name` label when the chart sets metrics.includeNames; without
it the pickers and legends fall back to IDs.
"""
import json
import os
import sys

DS = {"type": "prometheus", "uid": "${datasource}"}

GOOD, WARN, CRIT, BLUE = "#0ca30c", "#fab219", "#d03b3b", "#3987e5"

SITE = 'site=~"$site"'
NAMES = "* on (site, ap) group_left (name) dismist_ap_info"


class Layout:
    """Places panels left to right on Grafana's 24-column grid."""

    def __init__(self):
        self.x = self.y = self.row_h = 0
        self.panels = []
        self.next_id = 1

    def add(self, panel, w, h):
        if self.x + w > 24:
            self.x, self.y, self.row_h = 0, self.y + self.row_h, 0
        panel["id"] = self.next_id
        self.next_id += 1
        panel["gridPos"] = {"x": self.x, "y": self.y, "w": w, "h": h}
        self.x += w
        self.row_h = max(self.row_h, h)
        self.panels.append(panel)

    def row(self, title):
        if self.x:
            self.x, self.y, self.row_h = 0, self.y + self.row_h, 0
        self.panels.append({
            "id": self.next_id, "type": "row", "title": title, "collapsed": False,
            "gridPos": {"x": 0, "y": self.y, "w": 24, "h": 1}, "panels": [],
        })
        self.next_id += 1
        self.y += 1


def target(expr, legend="", ref="A", instant=False, fmt="time_series"):
    t = {"datasource": DS, "expr": expr, "refId": ref, "legendFormat": legend or "__auto",
         "range": not instant, "instant": instant, "format": fmt}
    return t


def thresholds(*steps):
    out = [{"color": steps[0], "value": None}]
    for value, color in steps[1:]:
        out.append({"color": color, "value": value})
    return {"mode": "absolute", "steps": out}


ONLINE = [{"type": "value", "options": {
    "1": {"text": "Online", "color": GOOD, "index": 0},
    "0": {"text": "Offline", "color": CRIT, "index": 1}}}]


def stat(title, expr, desc="", unit="none", th=None, mappings=None, legend="", text_mode="value", decimals=None, color_mode="value"):
    panel = {
        "type": "stat", "title": title, "description": desc, "datasource": DS,
        "targets": [target(expr, legend, instant=True)],
        "fieldConfig": {"defaults": {"unit": unit, "thresholds": th or thresholds(BLUE),
                                     "color": {"mode": "thresholds"}, "mappings": mappings or []},
                        "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": color_mode, "graphMode": "none", "justifyMode": "auto",
                    "textMode": text_mode, "orientation": "auto", "wideLayout": True},
    }
    if decimals is not None:
        panel["fieldConfig"]["defaults"]["decimals"] = decimals
    return panel


def timeseries(title, targets, desc="", unit="none", stack=False, bars=False, decimals=None, min0=True):
    custom = {"drawStyle": "bars" if bars else "line", "lineWidth": 2, "fillOpacity": 60 if bars else 10,
              "showPoints": "never", "spanNulls": False, "axisSoftMin": 0 if min0 else None,
              "stacking": {"mode": "normal" if stack else "none", "group": "A"}}
    defaults = {"unit": unit, "custom": custom, "color": {"mode": "palette-classic"}}
    if decimals is not None:
        defaults["decimals"] = decimals
    return {
        "type": "timeseries", "title": title, "description": desc, "datasource": DS, "targets": targets,
        "fieldConfig": {"defaults": defaults, "overrides": []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
    }


def state_timeline(title, expr, legend, desc=""):
    return {
        "type": "state-timeline", "title": title, "description": desc, "datasource": DS,
        "targets": [target(expr, legend)],
        "fieldConfig": {"defaults": {"mappings": ONLINE, "thresholds": thresholds(CRIT, (1, GOOD)),
                                     "color": {"mode": "thresholds"}, "custom": {"fillOpacity": 80, "lineWidth": 0}},
                        "overrides": []},
        "options": {"showValue": "never", "mergeValues": True, "rowHeight": 0.8, "alignValue": "left",
                    "legend": {"showLegend": False}, "tooltip": {"mode": "single"}},
    }


def variables(include_ap=False):
    v = [
        {"name": "datasource", "label": "Data source", "type": "datasource", "query": "prometheus",
         "current": {}, "hide": 0, "refresh": 1, "regex": "", "options": []},
        {"name": "namespace", "label": "Namespace", "type": "query", "datasource": DS,
         "query": {"query": "label_values(dismist_aps, namespace)", "refId": "ns"},
         "definition": "label_values(dismist_aps, namespace)",
         "refresh": 2, "includeAll": False, "multi": False, "hide": 0, "current": {}, "options": [], "sort": 1},
        {"name": "site", "label": "Site", "type": "query", "datasource": DS,
         "query": {"query": 'dismist_site_info{namespace="$namespace"}', "refId": "site"},
         "definition": 'dismist_site_info{namespace="$namespace"}',
         # Series render with labels in name order: name="…" comes before site="…".
         "regex": '/(?:name="(?<text>[^"]*)".*)?site="(?<value>[^"]+)"/',
         "refresh": 2, "includeAll": True, "allValue": ".*", "multi": True, "hide": 0,
         "current": {"selected": True, "text": ["All"], "value": ["$__all"]}, "options": [], "sort": 1},
    ]
    if include_ap:
        v.append({"name": "ap", "label": "Access point", "type": "query", "datasource": DS,
                  "query": {"query": 'dismist_ap_info{namespace="$namespace", site=~"$site"}', "refId": "ap"},
                  "definition": 'dismist_ap_info{namespace="$namespace", site=~"$site"}',
                  "regex": '/ap="(?<value>[^"]+)"(?:.*?name="(?<text>[^"]*)")?/',
                  "refresh": 2, "includeAll": False, "multi": False, "hide": 0, "current": {}, "options": [], "sort": 1})
    return v


def dashboard(uid, title, description, panels, templating, links):
    return {
        "uid": uid, "title": title, "description": description, "tags": ["dismist", "wifi"],
        "timezone": "browser", "editable": True, "graphTooltip": 1, "schemaVersion": 39, "version": 1,
        "refresh": "30s", "time": {"from": "now-24h", "to": "now"},
        "timepicker": {}, "fiscalYearStartMonth": 0, "liveNow": False, "weekStart": "",
        "templating": {"list": templating}, "annotations": {"list": []},
        "links": links, "panels": panels,
    }


def ns(selector=""):
    parts = ['namespace="$namespace"']
    if selector:
        parts.append(selector)
    return "{" + ", ".join(parts) + "}"


def overview():
    L = Layout()
    S = ns(SITE)
    L.row("Status")
    L.add(stat("APs online", f"sum(dismist_ap_connected{S}) or vector(0)",
               "Access points connected to the controller now.", th=thresholds(BLUE)), 4, 4)
    L.add(stat("APs registered", f"sum(dismist_site_aps{S}) or vector(0)",
               "Access points registered in the selected sites."), 4, 4)
    L.add(stat("APs offline", f"(sum(dismist_site_aps{S}) or vector(0)) - (sum(dismist_ap_connected{S}) or vector(0))",
               "Registered access points that are not connected.", th=thresholds(GOOD, (1, CRIT))), 4, 4)
    L.add(stat("Wi-Fi clients", f"sum(dismist_site_associated_clients{S}) or vector(0)",
               "Clients associated to connected access points."), 4, 4)
    L.add(stat("APs behind config",
               f"count((dismist_ap_desired_revision{S} - on (site, ap) dismist_ap_applied_revision{S}) > 0) or vector(0)",
               "Access points whose applied configuration revision is older than the desired one.",
               th=thresholds(GOOD, (1, WARN))), 4, 4)
    L.add(stat("Reconnects (24h)", f"sum(increase(dismist_ap_connections_total{S}[24h])) or vector(0)",
               "Access point (re)connections in the last 24 hours. Config saves and controller restarts also cause them.",
               th=thresholds(GOOD, (3, WARN), (10, CRIT)), decimals=0), 4, 4)

    L.row("Access points")
    table = {
        "type": "table", "title": "Access points", "datasource": DS,
        "description": "One row per access point. Click a name for its dashboard.",
        "targets": [
            target(f"dismist_ap_info{S}", ref="A", instant=True, fmt="table"),
            target(f"dismist_ap_connected{S}", ref="B", instant=True, fmt="table"),
            target(f"dismist_ap_uptime_seconds{S}", ref="C", instant=True, fmt="table"),
            target(f"dismist_ap_associated_clients{S}", ref="D", instant=True, fmt="table"),
            target(f"time() - (dismist_ap_last_seen_timestamp_seconds{S} > 0)", ref="E", instant=True, fmt="table"),
            target(f"dismist_ap_desired_revision{S} - on (site, ap) dismist_ap_applied_revision{S}", ref="F", instant=True, fmt="table"),
        ],
        "transformations": [
            {"id": "merge", "options": {}},
            {"id": "organize", "options": {
                "excludeByName": {"Time": True, "Value #A": True, "instance": True, "job": True, "namespace": True,
                                  "pod": True, "container": True, "endpoint": True, "service": True, "site": True,
                                  "__name__": True},
                "indexByName": {"name": 0, "ap": 1, "Value #B": 2, "Value #D": 3, "Value #C": 4, "Value #E": 5,
                                "Value #F": 6, "model": 7, "firmware": 8},
                "renameByName": {"name": "Name", "ap": "AP ID", "Value #B": "Status", "Value #D": "Clients",
                                 "Value #C": "Uptime", "Value #E": "Last message", "Value #F": "Config behind",
                                 "model": "Model", "firmware": "Firmware"}}},
        ],
        "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}}},
                        "overrides": [
                            {"matcher": {"id": "byName", "options": "Status"},
                             "properties": [{"id": "mappings", "value": ONLINE},
                                            {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}},
                                            {"id": "color", "value": {"mode": "thresholds"}},
                                            {"id": "thresholds", "value": thresholds(CRIT, (1, GOOD))}]},
                            {"matcher": {"id": "byName", "options": "Uptime"},
                             "properties": [{"id": "unit", "value": "dtdurations"}]},
                            {"matcher": {"id": "byName", "options": "Last message"},
                             "properties": [{"id": "unit", "value": "s"}, {"id": "decimals", "value": 0}]},
                            {"matcher": {"id": "byName", "options": "Config behind"},
                             "properties": [{"id": "custom.cellOptions", "value": {"type": "color-text"}},
                                            {"id": "color", "value": {"mode": "thresholds"}},
                                            {"id": "thresholds", "value": thresholds(GOOD, (1, WARN))}]},
                            {"matcher": {"id": "byName", "options": "Name"},
                             "properties": [{"id": "links", "value": [{
                                 "title": "Access point dashboard",
                                 "url": "/d/dismist-ap?var-namespace=${namespace}&var-site=${__data.fields.site}&var-ap=${__data.fields[\"AP ID\"]}&${__url_time_range}"}]}]},
                        ]},
        "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
    }
    L.add(table, 24, 7)

    L.row("Activity")
    L.add(timeseries("Clients per AP",
                     [target(f"sum by (ap, name) (dismist_ap_associated_clients{S} {NAMES})", "{{name}} {{ap}}")],
                     "Associated clients on each access point.", stack=True, decimals=0), 12, 8)
    L.add(state_timeline("AP status", f"dismist_ap_connected{S} {NAMES}", "{{name}} {{ap}}",
                         "Connected (green) or offline (red) over time."), 12, 8)
    L.add(timeseries("AP uptime", [target(f"dismist_ap_uptime_seconds{S} {NAMES}", "{{name}} {{ap}}")],
                     "Uptime the access point reports. A drop to zero is a reboot.", unit="dtdurations"), 8, 8)
    L.add(timeseries("Reconnects",
                     [target(f"sum by (ap, name) (increase(dismist_ap_connections_total{S}[$__rate_interval]) {NAMES})",
                             "{{name}} {{ap}}")],
                     "Access point connections per interval. Each config save currently reconnects the APs it changes.",
                     bars=True, decimals=0), 8, 8)
    L.add(timeseries("Telemetry frames",
                     [target(f"sum by (ap, name) (rate(dismist_ap_full_frames_total{S}[$__rate_interval]) {NAMES})",
                             "full · {{name}} {{ap}}", "A"),
                      target(f"sum by (ap, name) (rate(dismist_ap_fast_frames_total{S}[$__rate_interval]) {NAMES})",
                             "fast · {{name}} {{ap}}", "B")],
                     "Telemetry frames decoded per second. Full frames arrive about once a minute, fast ones every 2 s.",
                     unit="reqps"), 8, 8)

    L.row("Controller")
    P = ns()
    L.add(stat("Database schema", f"max(dismist_store_schema{P})", "Data format version of the controller database."), 4, 4)
    L.add(stat("Config compile errors", f"sum(dismist_ap_config_compile_error{S}) or vector(0)",
               "Access points whose last config compile failed; they keep their last delivered config.",
               th=thresholds(GOOD, (1, CRIT))), 4, 4)
    L.add(stat("Controller uptime", f"time() - max(process_start_time_seconds{P})",
               "Time since the dismistd process started.", unit="dtdurations"), 4, 4)
    L.add(stat("Memory (RSS)", f"max(process_resident_memory_bytes{P})", "Resident memory of dismistd.", unit="bytes"), 4, 4)
    L.add(stat("Goroutines", f"max(go_goroutines{P})", "Live goroutines in dismistd."), 4, 4)
    L.add(stat("Open files", f"max(process_open_fds{P})", "Open file descriptors of dismistd."), 4, 4)
    L.add(timeseries("Controller CPU", [target(f"rate(process_cpu_seconds_total{P}[$__rate_interval])", "cpu")],
                     "CPU used by dismistd, in cores.", unit="none", decimals=3), 12, 7)
    L.add(timeseries("Controller memory",
                     [target(f"process_resident_memory_bytes{P}", "resident", "A"),
                      target(f"go_memstats_heap_inuse_bytes{P}", "Go heap in use", "B")],
                     "Resident memory and Go heap of dismistd.", unit="bytes"), 12, 7)
    links = [{"title": "Access point", "type": "link", "url": "/d/dismist-ap?var-namespace=${namespace}&${__url_time_range}",
              "icon": "dashboard", "tooltip": "Per-AP dashboard", "targetBlank": False}]
    return dashboard("dismist-overview", "Dismist / Overview",
                     "Access points, clients and controller health for dismist.", L.panels, variables(), links)


def access_point():
    L = Layout()
    A = ns('site=~"$site", ap="$ap"')
    L.row("Status")
    L.add(stat("Status", f"max(dismist_ap_connected{A})", "Connected to the controller now.",
               th=thresholds(CRIT, (1, GOOD)), mappings=ONLINE, color_mode="background"), 4, 4)
    L.add(stat("Clients", f"max(dismist_ap_associated_clients{A})", "Associated clients now.", decimals=0), 4, 4)
    L.add(stat("Uptime", f"max(dismist_ap_uptime_seconds{A})", "Uptime the access point reports.", unit="dtdurations"), 4, 4)
    L.add(stat("Last message", f"time() - max(dismist_ap_last_seen_timestamp_seconds{A} > 0)",
               "Seconds since the controller last heard from it.", unit="s", decimals=0,
               th=thresholds(GOOD, (120, WARN), (600, CRIT))), 4, 4)
    L.add(stat("Model", f"max by (model) (dismist_ap_info{A})", "Model the access point reports.",
               legend="{{model}}", text_mode="name"), 4, 4)
    L.add(stat("Firmware", f"max by (firmware) (dismist_ap_info{A})", "Firmware the access point reports.",
               legend="{{firmware}}", text_mode="name"), 4, 4)
    L.add(stat("Desired revision", f"max(dismist_ap_desired_revision{A})", "Configuration revision dismist wants the AP to run.", decimals=0), 4, 4)
    L.add(stat("Applied revision", f"max(dismist_ap_applied_revision{A})", "Last revision the AP reported as applied.", decimals=0), 4, 4)
    L.add(stat("Reconnects (24h)", f"sum(increase(dismist_ap_connections_total{A}[24h])) or vector(0)",
               "Connections in the last 24 hours.", th=thresholds(GOOD, (3, WARN), (10, CRIT)), decimals=0), 4, 4)
    L.add(stat("Compile error", f"max(dismist_ap_config_compile_error{A}) or vector(0)",
               "Whether its last config compile failed.", th=thresholds(GOOD, (1, CRIT)),
               mappings=[{"type": "value", "options": {"0": {"text": "OK", "index": 0}, "1": {"text": "Failing", "index": 1}}}]), 4, 4)

    L.row("History")
    L.add(state_timeline("Status", f"dismist_ap_connected{A}", "status", "Connected (green) or offline (red)."), 24, 3)
    L.add(timeseries("Clients", [target(f"dismist_ap_associated_clients{A}", "clients")],
                     "Associated clients.", decimals=0), 12, 8)
    L.add(timeseries("Uptime", [target(f"dismist_ap_uptime_seconds{A}", "uptime")],
                     "A drop to zero is a reboot.", unit="dtdurations"), 12, 8)
    L.add(timeseries("Reconnects", [target(f"increase(dismist_ap_connections_total{A}[$__rate_interval])", "connections")],
                     "Connections per interval.", bars=True, decimals=0), 8, 8)
    L.add(timeseries("Config revisions",
                     [target(f"dismist_ap_desired_revision{A}", "desired", "A"),
                      target(f"dismist_ap_applied_revision{A}", "applied", "B")],
                     "Desired and applied configuration revisions.", decimals=0, min0=False), 8, 8)
    L.add(timeseries("Telemetry frames",
                     [target(f"rate(dismist_ap_full_frames_total{A}[$__rate_interval])", "full", "A"),
                      target(f"rate(dismist_ap_fast_frames_total{A}[$__rate_interval])", "fast", "B")],
                     "Telemetry frames decoded per second.", unit="reqps"), 8, 8)
    links = [{"title": "Overview", "type": "link", "url": "/d/dismist-overview?var-namespace=${namespace}&${__url_time_range}",
              "icon": "dashboard", "tooltip": "All access points", "targetBlank": False}]
    return dashboard("dismist-ap", "Dismist / Access point",
                     "One access point: status, clients, uptime, reconnects and config revisions.",
                     L.panels, variables(include_ap=True), links)


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for name, board in (("overview.json", overview()), ("access-point.json", access_point())):
        with open(os.path.join(out, name), "w") as f:
            json.dump(board, f, indent=1, sort_keys=True)
            f.write("\n")
        print("wrote", os.path.join(out, name))


if __name__ == "__main__":
    main()
