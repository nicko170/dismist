#!/usr/bin/env python3
"""Generate the dismist Grafana dashboards shipped in charts/dismist/monitoring/dashboards.

Usage: tools/dashboards/generate.py charts/dismist/monitoring/dashboards

Writes three dashboards (design §6.7): Dismist / Overview (dismist-overview),
Dismist / Access point (dismist-ap) and Dismist / Clients & RF (dismist-rf).
Stdlib only; the output is deterministic, and CI regenerates it and fails on
a difference.

The dashboards only query metrics dismistd emits: internal/metrics
(TestRulesAndDashboardsUseEmittedMetrics) checks every dismist_* name they
use. Names come from dismist_site_info, dismist_ap_info and
dismist_wlan_info, which carry a `name` label when the chart sets
metrics.includeNames; without it the pickers and legends fall back to IDs.
Telemetry series exist only while an AP reports, so gaps in a panel mean the
AP was disconnected or silent.
"""
import json
import os
import sys

DS = {"type": "prometheus", "uid": "${datasource}"}

# Status colours always come with text (design §6.7); download blue, upload orange.
GOOD, WARN, CRIT, BLUE, ORANGE, GREY = "#0ca30c", "#fab219", "#d03b3b", "#3987e5", "#d95926", "#8e8e8e"

NS = 'namespace="$namespace"'
SITE = 'site=~"$site"'
MIN_INTERVAL = "1m"  # full frames arrive every 60 s, scrapes every 30 s


def names(selector=NS):
    """Join the AP name (empty without -metrics-names) onto series keeping site and ap."""
    return f"* on (site, ap) group_left (name) max by (site, ap, name) (dismist_ap_info{{{selector}}})"


def wlan_names(selector=NS):
    return f"* on (wlan) group_left (name) max by (wlan, name) (dismist_wlan_info{{{selector}}})"


def m(key, op, value):
    """One label matcher, for use inside f-string expressions."""
    return f'{key}{op}"{value}"'


def sel(*parts):
    return "{" + ", ".join(p for p in parts if p) + "}"


class Layout:
    """Places panels left to right on Grafana's 24-column grid."""

    def __init__(self):
        self.x = self.y = self.row_h = 0
        self.panels = []
        self.next_id = 1
        self.collapsed = None

    def _newline(self):
        if self.x:
            self.x, self.y, self.row_h = 0, self.y + self.row_h, 0

    def add(self, panel, w, h):
        if self.x + w > 24:
            self._newline()
        panel["id"] = self.next_id
        self.next_id += 1
        panel["gridPos"] = {"x": self.x, "y": self.y, "w": w, "h": h}
        self.x += w
        self.row_h = max(self.row_h, h)
        if self.collapsed is not None:
            self.collapsed["panels"].append(panel)
        else:
            self.panels.append(panel)

    def row(self, title, repeat=None, collapsed=False):
        self._newline()
        row = {"id": self.next_id, "type": "row", "title": title, "collapsed": collapsed,
               "gridPos": {"x": 0, "y": self.y, "w": 24, "h": 1}, "panels": []}
        if repeat:
            row["repeat"] = repeat
        self.next_id += 1
        self.y += 1
        self.panels.append(row)
        # Panels after a collapsed row live inside it until the next row.
        self.collapsed = row if collapsed else None


def target(expr, legend="", ref="A", instant=False, fmt="time_series"):
    return {"datasource": DS, "expr": expr, "refId": ref, "legendFormat": legend or "__auto",
            "range": not instant, "instant": instant, "format": fmt}


def refs(*items):
    """targets from (expr, legend) pairs, lettered A, B, C..."""
    return [target(expr, legend, chr(ord("A") + i)) for i, (expr, legend) in enumerate(items)]


def thresholds(*steps):
    out = [{"color": steps[0], "value": None}]
    for value, color in steps[1:]:
        out.append({"color": color, "value": value})
    return {"mode": "absolute", "steps": out}


def value_map(**texts):
    """Value mappings: value_map(**{"1": ("Online", GOOD), "0": ("Offline", CRIT)})."""
    options = {}
    for index, (value, (text, color)) in enumerate(texts.items()):
        options[value] = {"text": text, "color": color, "index": index}
    return [{"type": "value", "options": options}]


ONLINE = value_map(**{"1": ("Online", GOOD), "0": ("Offline", CRIT)})
YES_NO_BAD = value_map(**{"0": ("No", GOOD), "1": ("Yes", CRIT)})
DUPLEX = value_map(**{"1": ("Full", GOOD), "0": ("Half", WARN)})
MIGRATION = value_map(**{"0": ("Committed or not run", GOOD), "1": ("Legacy mode", WARN)})


def stat(title, expr, desc="", unit="none", th=None, mappings=None, legend="", text_mode="value",
         decimals=None, color_mode="value", graph=False):
    panel = {
        "type": "stat", "title": title, "description": desc, "datasource": DS,
        "targets": [target(expr, legend, instant=not graph)],
        "fieldConfig": {"defaults": {"unit": unit, "thresholds": th or thresholds(BLUE),
                                     "color": {"mode": "thresholds"}, "mappings": mappings or []},
                        "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": color_mode, "graphMode": "area" if graph else "none", "justifyMode": "auto",
                    "textMode": text_mode, "orientation": "auto", "wideLayout": True},
    }
    if graph:
        panel["interval"] = MIN_INTERVAL
    if decimals is not None:
        panel["fieldConfig"]["defaults"]["decimals"] = decimals
    return panel


def color_override(match, color, regex=True):
    return {"matcher": {"id": "byRegexp" if regex else "byName", "options": match},
            "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": color}}]}


def timeseries(title, targets, desc="", unit="none", stack=False, bars=False, decimals=None, min0=True,
               overrides=None, th=None, step=False, percent=False, maximum=None):
    custom = {"drawStyle": "bars" if bars else "line", "lineWidth": 2, "fillOpacity": 60 if bars else (40 if stack else 10),
              "showPoints": "never", "spanNulls": False,
              "lineInterpolation": "stepAfter" if step else "linear",
              "stacking": {"mode": "percent" if percent else ("normal" if stack else "none"), "group": "A"}}
    if min0:
        custom["axisSoftMin"] = 0
    if th:
        custom["thresholdsStyle"] = {"mode": "line+area"}
    defaults = {"unit": unit, "custom": custom, "color": {"mode": "palette-classic"}}
    if th:
        defaults["thresholds"] = th
    if decimals is not None:
        defaults["decimals"] = decimals
    if maximum is not None:
        defaults["max"] = maximum
    return {
        "type": "timeseries", "title": title, "description": desc, "datasource": DS, "targets": targets,
        "interval": MIN_INTERVAL,
        "fieldConfig": {"defaults": defaults, "overrides": overrides or []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
    }


def state_timeline(title, expr, legend, desc="", mappings=None, th=None):
    return {
        "type": "state-timeline", "title": title, "description": desc, "datasource": DS,
        "targets": [target(expr, legend)], "interval": MIN_INTERVAL,
        "fieldConfig": {"defaults": {"mappings": mappings or ONLINE, "thresholds": th or thresholds(CRIT, (1, GOOD)),
                                     "color": {"mode": "thresholds"}, "custom": {"fillOpacity": 80, "lineWidth": 0}},
                        "overrides": []},
        "options": {"showValue": "never", "mergeValues": True, "rowHeight": 0.8, "alignValue": "left",
                    "legend": {"showLegend": False}, "tooltip": {"mode": "single"}},
    }


def bargauge(title, expr, legend, desc="", unit="none", th=None, decimals=None):
    defaults = {"unit": unit, "thresholds": th or thresholds(BLUE), "color": {"mode": "thresholds"}, "min": 0}
    if decimals is not None:
        defaults["decimals"] = decimals
    return {
        "type": "bargauge", "title": title, "description": desc, "datasource": DS,
        "targets": [target(expr, legend, instant=True)],
        "fieldConfig": {"defaults": defaults, "overrides": []},
        "options": {"orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
                    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "valueMode": "color", "namePlacement": "auto", "sizing": "auto"},
    }


HIDDEN_LABELS = {"Time": True, "instance": True, "job": True, "namespace": True, "pod": True, "container": True,
                 "endpoint": True, "service": True, "__name__": True}


def table(title, targets, desc, order, renames, overrides, hide=()):
    exclude = dict(HIDDEN_LABELS)
    for name in hide:
        exclude[name] = True
    return {
        "type": "table", "title": title, "datasource": DS, "description": desc, "targets": targets,
        "transformations": [
            {"id": "merge", "options": {}},
            {"id": "organize", "options": {"excludeByName": exclude, "indexByName": {n: i for i, n in enumerate(order)},
                                           "renameByName": renames}},
        ],
        "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}}}, "overrides": overrides},
        "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
    }


def column(name, *properties):
    return {"matcher": {"id": "byName", "options": name}, "properties": [{"id": k, "value": v} for k, v in properties]}


def colored(name, th, mappings=None, background=False, unit=None, decimals=None):
    props = [("custom.cellOptions", {"type": "color-background" if background else "color-text", "mode": "basic"}),
             ("color", {"mode": "thresholds"}), ("thresholds", th)]
    if mappings:
        props.append(("mappings", mappings))
    if unit:
        props.append(("unit", unit))
    if decimals is not None:
        props.append(("decimals", decimals))
    return column(name, *props)


def query_var(name, label, query, regex="", multi=False, include_all=False, all_value=None, current=None, hide=0):
    v = {"name": name, "label": label, "type": "query", "datasource": DS,
         "query": {"query": query, "refId": name}, "definition": query, "regex": regex,
         "refresh": 2, "includeAll": include_all, "multi": multi, "hide": hide,
         "current": current or {}, "options": [], "sort": 1}
    if all_value is not None:
        v["allValue"] = all_value
    return v


ALL = {"selected": True, "text": ["All"], "value": ["$__all"]}


def variables(ap="none"):
    """Shared variables; ap is "none", "single" (Access point) or "multi" (Clients & RF)."""
    v = [
        {"name": "datasource", "label": "Data source", "type": "datasource", "query": "prometheus",
         "current": {}, "hide": 0, "refresh": 1, "regex": "", "options": []},
        query_var("namespace", "Namespace", "label_values(dismist_build_info, namespace)"),
        # Series render with labels in name order: name="…" comes before site="…".
        query_var("site", "Site", f"dismist_site_info{{{NS}}}", regex='/(?:name="(?<text>[^"]*)".*)?site="(?<value>[^"]+)"/',
                  multi=True, include_all=True, all_value=".*", current=ALL),
    ]
    ap_query = f"dismist_ap_info{{{NS}, {SITE}}}"
    ap_regex = '/ap="(?<value>[^"]+)"(?:.*?name="(?<text>[^"]*)")?/'
    if ap == "single":
        v.append(query_var("ap", "Access point", ap_query, regex=ap_regex))
        v.append(query_var("radio", "Radio", f'label_values(dismist_radio_info{{{NS}, ap="$ap"}}, radio)',
                           multi=True, include_all=True, current=ALL, hide=2))
    elif ap == "multi":
        v.append(query_var("ap", "Access point", ap_query, regex=ap_regex, multi=True, include_all=True,
                           all_value=".*", current=ALL))
    return v


def annotation(name, expr, color, title):
    return {"datasource": DS, "enable": True, "expr": expr, "iconColor": color, "name": name, "step": "1m",
            "titleFormat": title, "textFormat": "", "useValueForTime": False, "tagKeys": ""}


BUILTIN_ANNOTATION = {"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True,
                      "hide": True, "iconColor": "rgba(0, 211, 255, 1)", "name": "Annotations & Alerts", "type": "dashboard"}


def dashboard(uid, title, description, panels, templating, links, annotations=()):
    return {
        "uid": uid, "title": title, "description": description, "tags": ["dismist", "wifi"],
        "timezone": "browser", "editable": True, "graphTooltip": 1, "schemaVersion": 39, "version": 1,
        "refresh": "30s", "time": {"from": "now-24h", "to": "now"},
        "timepicker": {}, "fiscalYearStartMonth": 0, "liveNow": False, "weekStart": "",
        "templating": {"list": templating}, "annotations": {"list": [BUILTIN_ANNOTATION] + list(annotations)},
        "links": links, "panels": panels,
    }


def link(title, uid, tooltip, extra=""):
    return {"title": title, "type": "link", "url": f"/d/{uid}?var-namespace=${{namespace}}{extra}&${{__url_time_range}}",
            "icon": "dashboard", "tooltip": tooltip, "targetBlank": False}


def cpu_busy(selector, by="site, ap"):
    idle = sel(selector, m("mode", "=", "idle"))
    return (f"1 - sum by ({by}) (rate(dismist_ap_cpu_seconds_total{idle}[$__rate_interval]))"
            f" / sum by ({by}) (rate(dismist_ap_cpu_seconds_total{sel(selector)}[$__rate_interval]))")


SIGNALS = [("poor", CRIT), ("fair", WARN), ("good", BLUE), ("excellent", GOOD)]


def overview():
    L = Layout()
    S = sel(NS, SITE)
    P = sel(NS)
    N = names()
    L.row("Status")
    L.add(stat("APs online", f"count(max by (site, ap) (dismist_ap_connected{S}) == 1) or vector(0)",
               "Access points connected to the controller now.", decimals=0), 3, 4)
    L.add(stat("APs registered", f"sum(dismist_ap_registered{S}) or vector(0)",
               "Access points with a control session (a secret and a config), so they can connect.", decimals=0), 3, 4)
    L.add(stat("APs offline",
               f"count((max by (site, ap) (dismist_ap_registered{S}) == 1) unless on (site, ap) (max by (site, ap) (dismist_ap_connected{S}) == 1)) or vector(0)",
               "Registered access points that are not connected.", th=thresholds(GOOD, (1, CRIT)), decimals=0), 3, 4)
    L.add(stat("Wi-Fi clients", f"sum(dismist_site_associated_clients{S}) or vector(0)",
               "Clients associated to connected access points.", decimals=0), 3, 4)
    L.add(stat("APs behind config",
               f"count((dismist_ap_desired_revision{S} - on (site, ap) dismist_ap_applied_revision{S}) > 0) or vector(0)",
               "Access points whose applied configuration revision is older than the desired one.",
               th=thresholds(GOOD, (1, WARN)), decimals=0), 3, 4)
    L.add(stat("AP certificate", f'(min(dismist_tls_certificate_not_after_timestamp_seconds{sel(NS, m("listener", "=", "ap-wss"))}) - time()) / 86400',
               "Days until the certificate dismist serves to access points expires. It renews 30 days before.",
               unit="d", th=thresholds(CRIT, (7, WARN), (14, GOOD)), decimals=0), 3, 4)
    L.add(stat("Reconnects (24h)", f"sum(increase(dismist_ap_connections_total{S}[24h])) or vector(0)",
               "Access point (re)connections in the last 24 hours. Reboots, config pushes in reconnect mode and controller restarts cause them.",
               th=thresholds(GOOD, (3, WARN), (10, CRIT)), decimals=0), 3, 4)
    L.add(stat("Auth / unknown APs (24h)",
               f'(sum(increase(dismist_ap_auth_failures_total{S}[24h])) or vector(0)) + (sum(increase(dismist_ap_ws_routes_total{sel(NS, m("result", "=", "unknown_ap"))}[24h])) or vector(0))',
               "Failed AP authentications plus hellos from access points dismist does not know, in the last 24 hours.",
               th=thresholds(GOOD, (1, WARN)), decimals=0), 3, 4)

    L.row("Access points")
    by = "site, ap"
    L.add(table("Access points", [
        target(f"max by (site, ap, name, model, firmware) (dismist_ap_info{S})", ref="A", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_ap_connected{S})", ref="B", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_ap_uptime_seconds{S})", ref="C", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_ap_associated_clients{S})", ref="D", instant=True, fmt="table"),
        target(f'max by ({by}) (dismist_ap_temperature_celsius{sel(NS, SITE, m("sensor", "=", "cpu"))})', ref="E", instant=True, fmt="table"),
        target(cpu_busy(f"{NS}, {SITE}"), ref="F", instant=True, fmt="table"),
        target(f"time() - max by ({by}) (dismist_ap_last_seen_timestamp_seconds{S} > 0)", ref="G", instant=True, fmt="table"),
        target(f'max by ({by}) (dismist_ap_controller_rtt_seconds{sel(NS, SITE, m("socket", "=", "tcp"))})', ref="H", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_ap_desired_revision{S}) - on (site, ap) max by ({by}) (dismist_ap_applied_revision{S})", ref="I", instant=True, fmt="table"),
        target(f'max by ({by}) (dismist_ap_port_speed_bits_per_second{sel(NS, SITE, m("port", "=", "eth0"))})', ref="J", instant=True, fmt="table"),
    ], "One row per access point. Click a name for its dashboard. Health columns stay empty while an AP is not reporting.",
        ["name", "ap", "Value #B", "Value #D", "Value #C", "Value #E", "Value #F", "Value #H", "Value #J", "Value #G", "Value #I", "model", "firmware"],
        {"name": "Name", "ap": "AP ID", "Value #B": "Status", "Value #C": "Uptime", "Value #D": "Clients",
         "Value #E": "CPU °C", "Value #F": "CPU busy", "Value #G": "Last message", "Value #H": "RTT",
         "Value #I": "Config behind", "Value #J": "Uplink", "model": "Model", "firmware": "Firmware"},
        [colored("Status", thresholds(CRIT, (1, GOOD)), ONLINE, background=True),
         column("Uptime", ("unit", "dtdurations")),
         colored("CPU °C", thresholds(GOOD, (70, WARN), (80, CRIT)), unit="celsius", decimals=0),
         colored("CPU busy", thresholds(GOOD, (0.7, WARN), (0.9, CRIT)), unit="percentunit", decimals=0),
         colored("RTT", thresholds(GOOD, (0.05, WARN), (0.1, CRIT)), unit="s"),
         colored("Uplink", thresholds(CRIT, (1e8, WARN), (1e9, GOOD)), unit="bps"),
         column("Last message", ("unit", "s"), ("decimals", 0)),
         colored("Config behind", thresholds(GOOD, (1, WARN)), decimals=0),
         column("Name", ("links", [{"title": "Access point dashboard",
                                    "url": "/d/dismist-ap?var-namespace=${namespace}&var-site=${__data.fields.site}&var-ap=${__data.fields[\"AP ID\"]}&${__url_time_range}"}]))],
        hide=("site", "Value #A")), 24, 8)

    L.row("Clients")
    L.add(timeseries("Clients per AP", [target(f"sum by (site, ap, name) (dismist_ap_associated_clients{S} {N})", "{{name}} {{ap}}")],
                     "Associated clients on each access point.", stack=True, decimals=0), 8, 8)
    L.add(timeseries("Clients by band", [target(f'sum by (band) (dismist_radio_clients{sel(NS, SITE, m("band", "!=", "scan"))})', "{{band}} GHz")],
                     "Clients on the 2.4 and 5 GHz access radios.", stack=True, decimals=0), 8, 8)
    L.add(timeseries("Clients by signal", [target(f'sum(dismist_radio_clients_by_signal{sel(NS, SITE, m("signal", "=", s))})', s, chr(ord("A") + i)) for i, (s, _) in enumerate(SIGNALS)],
                     "Clients by signal strength: excellent -60 dBm or better, good to -67, fair to -75, poor below.",
                     stack=True, decimals=0, overrides=[color_override(s, c, regex=False) for s, c in SIGNALS]), 8, 8)

    L.row("Activity")
    L.add(state_timeline("AP connectivity", f"max by (site, ap, name) (dismist_ap_connected{S} {N})", "{{name}} {{ap}}",
                         "Connected (green) or offline (red) over time."), 12, 8)
    L.add(timeseries("Reconnects", [target(f"sum by (site, ap, name) (increase(dismist_ap_connections_total{S}[$__rate_interval]) {N})", "{{name}} {{ap}}")],
                     "Access point connections per interval.", bars=True, decimals=0), 12, 8)
    L.add(timeseries("Uplink throughput", [
        target(f'sum by (site, ap, name) (rate(dismist_ap_port_receive_bytes_total{sel(NS, SITE, m("port", "=", "eth0"))}[$__rate_interval]) * 8 {N})', "rx {{name}} {{ap}}", "A"),
        target(f'-sum by (site, ap, name) (rate(dismist_ap_port_transmit_bytes_total{sel(NS, SITE, m("port", "=", "eth0"))}[$__rate_interval]) * 8 {N})', "tx {{name}} {{ap}}", "B"),
    ], "eth0 traffic: received from the network above the line, sent below. The AP's transmit counter may leave out its own traffic.",
        unit="bps", min0=False, overrides=[color_override("^rx .*", BLUE), color_override("^tx .*", ORANGE)]), 12, 8)
    L.add(timeseries("Channel utilisation", [target(f'max by (site, ap, name, radio, band) (dismist_radio_channel_utilization_ratio{sel(NS, SITE, m("type", "=", "all"), m("band", "!=", "scan"))} {N})', "{{name}} {{ap}} {{radio}} ({{band}} GHz)")],
                     "How busy each access radio's channel is, by anyone. Over 0.7 for long is congested.",
                     unit="percentunit", maximum=1, th=thresholds(GOOD, (0.5, WARN), (0.7, CRIT))), 12, 8)

    L.row("Controller")
    L.add(stat("Version", f"max by (version) (dismist_build_info{P})", "dismistd build.", legend="{{version}}", text_mode="name"), 4, 4)
    L.add(stat("Controller uptime", f"time() - max(process_start_time_seconds{P})", "Time since the dismistd process started.", unit="dtdurations"), 4, 4)
    L.add(stat("Database", f"max(dismist_store_size_bytes{P})", "Size of the controller database.", unit="bytes"), 4, 4)
    L.add(stat("Volume free", f"max(dismist_store_free_bytes{P})", "Free space on the database volume.", unit="bytes"), 4, 4)
    L.add(stat("Last backup", f"time() - max(dismist_backup_last_success_timestamp_seconds{P})",
               "Time since the last scheduled snapshot succeeded.", unit="dtdurations", th=thresholds(GOOD, (86400 * 1.5, WARN), (86400 * 2, CRIT))), 4, 4)
    L.add(stat("Migration", f'max(dismist_schema_migration_state{sel(NS, m("state", "=", "legacy"))}) or vector(0)',
               "Legacy mode means the schema 2 migration did not verify and typed settings are paused (System, Migration).",
               th=thresholds(GOOD, (1, WARN)), mappings=MIGRATION), 4, 4)
    L.add(timeseries("Controller CPU", [target(f"rate(process_cpu_seconds_total{P}[$__rate_interval])", "cpu")],
                     "CPU used by dismistd, in cores.", decimals=3), 8, 7)
    L.add(timeseries("Controller memory", refs((f"process_resident_memory_bytes{P}", "resident"), (f"go_memstats_heap_inuse_bytes{P}", "Go heap in use")),
                     "Resident memory and Go heap of dismistd.", unit="bytes"), 8, 7)
    L.add(timeseries("Goroutines and UI streams", refs((f"go_goroutines{P}", "goroutines"), (f"dismist_ui_streams{P}", "UI event streams")),
                     "Live goroutines in dismistd and open operator UI event streams.", decimals=0), 8, 7)
    L.add(timeseries("Telemetry frames", refs(
        (f"sum(rate(dismist_ap_fast_frames_total{S}[$__rate_interval]))", "fast"),
        (f"sum(rate(dismist_ap_full_frames_total{S}[$__rate_interval]))", "full")),
        "Telemetry frames decoded per second over all APs: fast every 2 s, full about once a minute.", unit="reqps"), 8, 7)
    L.add(timeseries("Decode errors", [target(f"sum by (section) (rate(dismist_ap_telemetry_decode_errors_total{S}[$__rate_interval]))", "{{section}}")],
                     "Telemetry sections that failed to decode, per second. A rise after a firmware change is expected; report it.",
                     unit="reqps"), 8, 7)
    L.add(timeseries("UI requests", [target(f"sum by (code) (rate(dismist_http_requests_total{P}[$__rate_interval]))", "{{code}}")],
                     "Operator UI and API requests per second by status code.", unit="reqps", stack=True), 8, 7)
    links = [link("Access point", "dismist-ap", "Per-AP dashboard"), link("Clients & RF", "dismist-rf", "Channels, interference and client quality")]
    return dashboard("dismist-overview", "Dismist / Overview",
                     "Access points, clients and controller health for dismist.", L.panels, variables(), links)


def access_point():
    L = Layout()
    A = sel(NS, SITE, 'ap="$ap"')
    R = sel(NS, SITE, 'ap="$ap"', 'radio="$radio"')

    def a(*extra):
        return sel(NS, SITE, 'ap="$ap"', *extra)

    L.row("Status")
    L.add(stat("Status", f"max(dismist_ap_connected{A})", "Connected to the controller now.",
               th=thresholds(CRIT, (1, GOOD)), mappings=ONLINE, color_mode="background"), 3, 4)
    L.add(stat("Uptime", f"max(dismist_ap_uptime_seconds{A})", "Uptime the access point reports.", unit="dtdurations"), 3, 4)
    L.add(stat("Model", f"max by (model) (dismist_ap_info{A})", "Model the access point reports.", legend="{{model}}", text_mode="name"), 3, 4)
    L.add(stat("Firmware", f"max by (firmware) (dismist_ap_info{A})", "Firmware the access point reports.", legend="{{firmware}}", text_mode="name"), 3, 4)
    L.add(stat("Boots (7d)", f"max(increase(dismist_ap_boots_total{A}[7d])) or vector(0)", "Reboots in the last 7 days.",
               th=thresholds(GOOD, (1, WARN), (3, CRIT)), decimals=0), 3, 4)
    L.add(stat("Clients", f"max(dismist_ap_associated_clients{A})", "Associated clients now.", decimals=0), 3, 4)
    L.add(stat("CPU temperature", f'max(dismist_ap_temperature_celsius{a(m("sensor", "=", "cpu"))})',
               "CPU temperature. 50 to 55 °C is normal.", unit="celsius", th=thresholds(GOOD, (70, WARN), (80, CRIT)), decimals=1), 3, 4)
    L.add(stat("CPU busy", cpu_busy(f'{NS}, {SITE}, ap="$ap"', by="ap"), "Share of CPU time not idle.",
               unit="percentunit", th=thresholds(GOOD, (0.7, WARN), (0.9, CRIT)), decimals=0), 3, 4)
    L.add(stat("Memory used", f"1 - max(dismist_ap_memory_available_bytes{A}) / max(dismist_ap_memory_total_bytes{A})",
               "Memory in use.", unit="percentunit", th=thresholds(GOOD, (0.8, WARN), (0.9, CRIT)), decimals=0), 3, 4)
    L.add(stat("/rw free", f"max(dismist_ap_rw_filesystem_available_bytes{A}) / max(dismist_ap_rw_filesystem_size_bytes{A})",
               "Free space on the AP's small writable filesystem.", unit="percentunit", th=thresholds(CRIT, (0.1, WARN), (0.25, GOOD)), decimals=0), 3, 4)
    L.add(stat("Uplink", f'max(dismist_ap_port_speed_bits_per_second{a(m("port", "=", "eth0"))})',
               "eth0 negotiated speed.", unit="bps", th=thresholds(CRIT, (1e8, WARN), (1e9, GOOD))), 3, 4)
    L.add(stat("Duplex", f'max(dismist_ap_port_full_duplex{a(m("port", "=", "eth0"))})',
               "eth0 duplex.", mappings=DUPLEX, th=thresholds(WARN, (1, GOOD))), 3, 4)
    L.add(stat("PoE source", f"max by (source) (dismist_ap_power_source_info{A})", "Where the AP gets its power budget from.",
               legend="{{source}}", text_mode="name"), 3, 4)
    L.add(stat("PoE allocated", f'max(dismist_ap_lldp_power_allocated_watts{a(m("port", "=", "eth0"))})',
               "Power the switch allocates over LLDP.", unit="watt", decimals=1), 3, 4)
    L.add(stat("PoE needed", f"max(dismist_ap_power_needed_watts{A})", "Power the AP needs for full operation.", unit="watt", decimals=1), 3, 4)
    L.add(stat("Controller RTT", f'max(dismist_ap_controller_rtt_seconds{a(m("socket", "=", "tcp"))})',
               "Round trip the AP measures to dismist. 3 to 10 ms is normal.", unit="s", th=thresholds(GOOD, (0.05, WARN), (0.1, CRIT))), 3, 4)
    L.add(stat("Last message", f"time() - max(dismist_ap_last_seen_timestamp_seconds{A} > 0)", "Seconds since the controller last heard from it.",
               unit="s", decimals=0, th=thresholds(GOOD, (120, WARN), (600, CRIT))), 3, 4)
    L.add(stat("Config revision", f"max(dismist_ap_applied_revision{A})", "Revision the AP reported applied; red while it is behind the desired one.",
               decimals=0, th=thresholds(GOOD)), 3, 4)
    L.add(stat("Config behind", f"max(dismist_ap_desired_revision{A}) - max(dismist_ap_applied_revision{A})", "Revisions not yet applied.",
               decimals=0, th=thresholds(GOOD, (1, WARN))), 3, 4)
    L.add(stat("Config rejected", f"max(dismist_ap_config_error{A}) or vector(0)", "Whether the AP rejected its last config.",
               th=thresholds(GOOD, (1, CRIT)), mappings=YES_NO_BAD), 3, 4)
    L.add(stat("Compile error", f"max(dismist_ap_config_compile_error{A}) or vector(0)", "Whether dismist failed to compile its config.",
               th=thresholds(GOOD, (1, CRIT)), mappings=YES_NO_BAD), 3, 4)
    L.add(stat("Verify mismatch", f"max(dismist_ap_config_verify_mismatch{A}) or vector(0)",
               "Whether the AP's telemetry disagrees with the config last delivered.", th=thresholds(GOOD, (1, CRIT)), mappings=YES_NO_BAD), 3, 4)
    L.add(stat("Reconnects (24h)", f"sum(increase(dismist_ap_connections_total{A}[24h])) or vector(0)",
               "Connections in the last 24 hours.", th=thresholds(GOOD, (3, WARN), (10, CRIT)), decimals=0), 3, 4)
    L.add(stat("Clock skew", f"max(dismist_ap_clock_skew_seconds{A})", "Controller time minus AP time.",
               unit="s", th=thresholds(GOOD, (30, WARN)), decimals=1), 3, 4)

    L.row("Health")
    L.add(state_timeline("Status", f"max(dismist_ap_connected{A})", "status", "Connected (green) or offline (red)."), 24, 3)
    L.add(timeseries("CPU by mode", [target(f'sum by (mode) (rate(dismist_ap_cpu_seconds_total{a(m("mode", "!=", "idle"))}[$__rate_interval])) / scalar(sum(rate(dismist_ap_cpu_seconds_total{A}[$__rate_interval])))', "{{mode}}")],
                     "Share of CPU time by mode, idle left out.", unit="percentunit", stack=True), 8, 8)
    L.add(timeseries("Load average", [target(f"max by (period) (dismist_ap_load_average{A})", "{{period}}")], "Load average over 1, 5 and 15 minutes (4 cores)."), 8, 8)
    L.add(timeseries("Memory", refs((f"max(dismist_ap_memory_total_bytes{A})", "total"), (f"max(dismist_ap_memory_available_bytes{A})", "available")),
                     "Memory and memory available.", unit="bytes"), 8, 8)
    L.add(timeseries("Temperature", [target(f"max by (sensor) (dismist_ap_temperature_celsius{A})", "{{sensor}}")],
                     "Temperatures the AP reports.", unit="celsius", min0=False, th=thresholds(GOOD, (70, WARN), (80, CRIT))), 8, 8)
    L.add(timeseries("Agent memory", refs((f"max(dismist_ap_agent_heap_bytes{A})", "heap"), (f"max(dismist_ap_agent_rss_bytes{A})", "resident")),
                     "Memory of the AP's telemetry agent; steady growth is a leak.", unit="bytes"), 8, 8)
    L.add(timeseries("Controller RTT", [target(f"max by (socket) (dismist_ap_controller_rtt_seconds{A})", "{{socket}}")],
                     "Round trip the AP measures to dismist.", unit="s"), 8, 8)

    L.row("Uplink and power")
    E = a(m("port", "=", "eth0"))
    L.add(timeseries("Uplink throughput", [
        target(f"sum(rate(dismist_ap_port_receive_bytes_total{E}[$__rate_interval])) * 8", "rx", "A"),
        target(f"-sum(rate(dismist_ap_port_transmit_bytes_total{E}[$__rate_interval])) * 8", "tx", "B"),
    ], "eth0: received above the line, sent below (the transmit counter may leave out the AP's own traffic).",
        unit="bps", min0=False, overrides=[color_override("rx", BLUE, regex=False), color_override("tx", ORANGE, regex=False)]), 8, 8)
    L.add(timeseries("Uplink packets", [
        target(f"sum(rate(dismist_ap_port_receive_packets_total{E}[$__rate_interval]))", "rx", "A"),
        target(f"-sum(rate(dismist_ap_port_transmit_packets_total{E}[$__rate_interval]))", "tx", "B"),
    ], "eth0 packets per second.", unit="pps", min0=False, overrides=[color_override("rx", BLUE, regex=False), color_override("tx", ORANGE, regex=False)]), 8, 8)
    L.add(timeseries("Uplink errors", refs(
        (f"sum by (port) (rate(dismist_ap_port_receive_errors_total{A}[$__rate_interval]))", "rx errors {{port}}"),
        (f"sum by (port) (rate(dismist_ap_port_receive_fcs_errors_total{A}[$__rate_interval]))", "bad checksum {{port}}"),
        (f"sum by (port) (rate(dismist_ap_port_receive_discards_total{A}[$__rate_interval]))", "discards {{port}}"),
        (f"sum by (port) (rate(dismist_ap_port_transmit_errors_total{A}[$__rate_interval]))", "tx errors {{port}}")),
        "Errors per second. Bad checksums point at the cable.", unit="pps"), 8, 8)
    L.add(timeseries("PoE", refs(
        (f"max(dismist_ap_power_available_watts{A})", "available"), (f"max(dismist_ap_power_needed_watts{A})", "needed"),
        (f"max(dismist_ap_power_draw_watts{A})", "measured draw"), (f"max(dismist_ap_lldp_power_allocated_watts{E})", "LLDP allocated"),
        (f"max(dismist_ap_lldp_power_requested_watts{E})", "LLDP requested"), (f"max(dismist_ap_power_budget_watts{A})", "budget left")),
        "Power budget. A negative budget left means the AP is short and limits its radios.", unit="watt", min0=False), 24, 8)

    L.row("History")
    L.add(timeseries("Clients", [target(f"max(dismist_ap_associated_clients{A})", "clients")], "Associated clients.", decimals=0), 8, 8)
    L.add(timeseries("Uptime", [target(f"max(dismist_ap_uptime_seconds{A})", "uptime")], "A drop is a reboot.", unit="dtdurations"), 8, 8)
    L.add(timeseries("Reconnects", [target(f"sum(increase(dismist_ap_connections_total{A}[$__rate_interval]))", "connections")],
                     "Connections per interval.", bars=True, decimals=0), 8, 8)
    L.add(timeseries("Config revisions", refs((f"max(dismist_ap_desired_revision{A})", "desired"), (f"max(dismist_ap_applied_revision{A})", "applied")),
                     "Desired and applied configuration revisions.", decimals=0, min0=False, step=True), 8, 8)
    L.add(timeseries("Telemetry frames", refs(
        (f"sum(rate(dismist_ap_full_frames_total{A}[$__rate_interval]))", "full"),
        (f"sum(rate(dismist_ap_fast_frames_total{A}[$__rate_interval]))", "fast"),
        (f"sum by (section) (rate(dismist_ap_telemetry_decode_errors_total{A}[$__rate_interval]))", "decode errors {{section}}")),
        "Telemetry frames decoded and sections that failed to decode, per second.", unit="reqps"), 8, 8)
    L.add(timeseries("Protocol", refs(
        (f"sum by (subject) (rate(dismist_ap_frames_total{A}[$__rate_interval]))", "{{subject}}"),
        (f"sum by (mode, result) (increase(dismist_ap_config_pushes_total{A}[$__rate_interval]))", "push {{mode}} {{result}}")),
        "Frames from the AP by subject per second, and config pushes.", unit="reqps"), 8, 8)

    L.row("Radio $radio", repeat="radio")
    L.add(stat("Channel", f"max(dismist_radio_channel{R})", "Primary channel.", decimals=0), 3, 4)
    L.add(stat("Width", f"max(dismist_radio_bandwidth_mhz{R})", "Channel width.", unit="MHz", decimals=0), 3, 4)
    L.add(stat("Max power", f"max(dismist_radio_max_tx_power_dbm{R})", "Transmit power.", unit="dBm", decimals=0), 3, 4)
    L.add(stat("Noise floor", f"max(dismist_radio_noise_floor_dbm{R})", "Noise floor; above -80 dBm is high.", unit="dBm",
               th=thresholds(GOOD, (-80, WARN)), decimals=0), 3, 4)
    L.add(stat("Clients", f"max(dismist_radio_clients{R})", "Clients on this radio.", decimals=0), 3, 4)
    L.add(stat("Role", f"max by (role, band) (dismist_radio_info{R})", "Access radios serve clients; the scan radio scans neighbours.",
               legend="{{role}} ({{band}})", text_mode="name"), 3, 4)
    L.add(stat("Utilisation", f'max(dismist_radio_channel_utilization_ratio{sel(NS, SITE, m("ap", "=", "$ap"), m("radio", "=", "$radio"), m("type", "=", "all"))})',
               "Channel busy now.", unit="percentunit", th=thresholds(GOOD, (0.5, WARN), (0.7, CRIT)), decimals=0), 3, 4)
    L.add(stat("Missing", f"max(dismist_radio_missing{R}) or vector(0)", "Whether the AP reports the radio missing.",
               th=thresholds(GOOD, (1, CRIT)), mappings=YES_NO_BAD), 3, 4)
    L.add(timeseries("Channel and width", refs((f"max(dismist_radio_channel{R})", "channel"), (f"max(dismist_radio_bandwidth_mhz{R})", "width MHz")),
                     "Channel and width over time; a step is a channel change.", decimals=0, step=True), 8, 8)
    L.add(timeseries("Utilisation by type", [target(f'max by (type) (dismist_radio_channel_utilization_ratio{sel(NS, SITE, m("ap", "=", "$ap"), m("radio", "=", "$radio"), m("type", "!=", "all"))})', "{{type}}")],
                     "Who keeps the channel busy: this AP (tx), its clients (rx_in_bss), neighbours (rx_other_bss), other Wi-Fi and non-Wi-Fi energy.",
                     unit="percentunit", stack=True), 8, 8)
    L.add(timeseries("Noise and power", refs((f"max(dismist_radio_noise_floor_dbm{R})", "noise floor"), (f"max(dismist_radio_max_tx_power_dbm{R})", "max power")),
                     "Noise floor and transmit power.", unit="dBm", min0=False), 8, 8)
    L.add(timeseries("Clients", [target(f"max(dismist_radio_clients{R})", "clients")], "Clients on this radio.", decimals=0), 8, 8)
    L.add(timeseries("Retries and failures", refs(
        (f"sum(rate(dismist_radio_tx_retries_total{R}[$__rate_interval]))", "tx retries"),
        (f"sum(rate(dismist_radio_tx_failed_total{R}[$__rate_interval]))", "tx failed")),
        "Retransmissions and failed transmissions per second.", unit="pps"), 8, 8)
    L.add(timeseries("Receive errors and beacons", refs(
        (f"sum(rate(dismist_radio_rx_glitches_total{R}[$__rate_interval]))", "glitches"),
        (f"sum(rate(dismist_radio_rx_bad_fcs_total{R}[$__rate_interval]))", "bad checksum"),
        (f"sum(rate(dismist_radio_beacons_total{R}[$__rate_interval]))", "beacons")),
        "Receive glitches and bad frames per second, and beacons sent (about 10 per second per WLAN).", unit="pps"), 8, 8)

    L.row("WLANs")
    by = "wlan, radio"
    W = wlan_names()
    L.add(table("WLANs on this AP", [
        target(f"sum by ({by}) (dismist_wlan_clients{A}) {W}", ref="A", instant=True, fmt="table"),
        target(f"sum by ({by}) (increase(dismist_wlan_eapol_invalid_mic_total{A}[1h])) {W}", ref="B", instant=True, fmt="table"),
        target(f"sum by ({by}) (rate(dismist_wlan_tx_multicast_bytes_total{A}[5m])) * 8 {W}", ref="C", instant=True, fmt="table"),
    ], "One row per WLAN and radio. Wrong passphrases are WPA handshakes that failed the passphrase check in the last hour.",
        ["name", "wlan", "radio", "Value #A", "Value #B", "Value #C"],
        {"name": "WLAN", "wlan": "Network ID", "radio": "Radio", "Value #A": "Clients", "Value #B": "Wrong passphrase (1h)", "Value #C": "Multicast"},
        [colored("Wrong passphrase (1h)", thresholds(GOOD, (1, WARN), (10, CRIT)), decimals=0),
         column("Clients", ("decimals", 0)), column("Multicast", ("unit", "bps"))]), 24, 6)
    links = [link("Overview", "dismist-overview", "All access points"),
             link("Clients & RF", "dismist-rf", "Channels and clients", "&var-ap=${ap}")]
    annotations = [
        annotation("Reconnects", f"changes(dismist_ap_connections_total{A}[2m]) > 0", ORANGE, "Reconnected"),
        annotation("Reboots", f"increase(dismist_ap_boots_total{A}[2m]) > 0", CRIT, "Rebooted"),
        annotation("Config applied", f"changes(dismist_ap_applied_revision{A}[2m]) > 0", BLUE, "Config revision applied"),
        annotation("Channel changes", f'changes(dismist_radio_channel{a(m("band", "!=", "scan"))}[2m]) > 0', WARN, "Channel changed ({{radio}})"),
    ]
    return dashboard("dismist-ap", "Dismist / Access point",
                     "One access point: health, uplink and PoE, history, each radio and its WLANs.",
                     L.panels, variables(ap="single"), links, annotations)


def clients_rf():
    L = Layout()
    R = sel(NS, SITE, 'ap=~"$ap"')
    ACCESS = 'band!="scan"'
    RA = sel(NS, SITE, 'ap=~"$ap"', ACCESS)
    N = names()
    W = wlan_names()

    def r(*extra):
        return sel(NS, SITE, 'ap=~"$ap"', *extra)

    L.row("Channel plan")
    by = "site, ap, radio, band"
    L.add(table("Channel plan", [
        target(f"max by ({by}) (dismist_radio_channel{RA}) {N}", ref="A", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_radio_bandwidth_mhz{RA}) {N}", ref="B", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_radio_noise_floor_dbm{RA}) {N}", ref="C", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_radio_max_tx_power_dbm{RA}) {N}", ref="D", instant=True, fmt="table"),
        target(f'max by ({by}) (dismist_radio_channel_utilization_ratio{r(ACCESS, m("type", "=", "all"))}) {N}', ref="E", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_radio_clients{RA}) {N}", ref="F", instant=True, fmt="table"),
        target(f"(max by (site, ap, band) (dismist_radio_planned_channel{R}) * on (site, ap, band) group_right () (max by ({by}) (dismist_radio_channel{RA}) * 0 + 1)) {N}", ref="G", instant=True, fmt="table"),
    ], "Access radios only. APs on the same channel and band share airtime; the planned column is dismist's channel plan.",
        ["name", "ap", "radio", "band", "Value #A", "Value #G", "Value #B", "Value #E", "Value #C", "Value #D", "Value #F"],
        {"name": "AP", "ap": "AP ID", "radio": "Radio", "band": "Band (GHz)", "Value #A": "Channel", "Value #G": "Planned",
         "Value #B": "Width", "Value #C": "Noise", "Value #D": "Power", "Value #E": "Utilisation", "Value #F": "Clients"},
        [column("Width", ("unit", "MHz")),
         colored("Noise", thresholds(GOOD, (-80, WARN)), unit="dBm", decimals=0),
         column("Power", ("unit", "dBm")),
         colored("Utilisation", thresholds(GOOD, (0.5, WARN), (0.7, CRIT)), unit="percentunit", decimals=0, background=True),
         column("Clients", ("decimals", 0))],
        hide=("site",)), 24, 8)

    L.row("Channels and interference")
    L.add(timeseries("Channel utilisation", [target(f'max by (site, ap, name, radio, band) (dismist_radio_channel_utilization_ratio{r(ACCESS, m("type", "=", "all"))} {N})', "{{name}} {{ap}} {{radio}} ({{band}} GHz)")],
                     "How busy each access radio's channel is. Over 0.5 is busy, over 0.7 congested.",
                     unit="percentunit", maximum=1, th=thresholds(GOOD, (0.5, WARN), (0.7, CRIT))), 12, 8)
    L.add(timeseries("Interference", [target(f'avg by (type) (dismist_radio_channel_utilization_ratio{r(ACCESS, m("type", "=~", "rx_other_bss|unknown_wifi|non_wifi|undecodable_wifi"))})', "{{type}}")],
                     "Airtime used by others, averaged over the selected access radios: neighbouring networks (rx_other_bss), Wi-Fi the radio cannot attribute, and non-Wi-Fi energy such as microwaves.",
                     unit="percentunit", stack=True), 12, 8)
    L.add(timeseries("Noise floor", [target(f"max by (site, ap, name, radio, band) (dismist_radio_noise_floor_dbm{RA} {N})", "{{name}} {{ap}} {{radio}} ({{band}} GHz)")],
                     "Noise floor per access radio. Above -80 dBm hurts every client.", unit="dBm", min0=False,
                     th=thresholds(GOOD, (-80, WARN))), 12, 8)
    L.add(timeseries("Channel changes", [target(f"sum by (site, ap, name, radio, band) (changes(dismist_radio_channel{RA}[1h]) {N})", "{{name}} {{ap}} {{radio}}")],
                     "Channel changes per hour: plan applies, DFS moves after radar.", bars=True, decimals=0), 12, 8)

    L.row("Client quality")
    L.add(bargauge("Clients by signal", f"sum by (site, ap, name, signal) (dismist_radio_clients_by_signal{R} {N})", "{{name}} {{ap}} {{signal}}",
                   "Clients on each AP by signal: excellent -60 dBm or better, good to -67, fair to -75, poor below.", decimals=0), 8, 10)
    L.add(timeseries("Clients by protocol", [target(f"sum by (protocol) (dismist_radio_clients_by_protocol{R})", "{{protocol}}")],
                     "Clients by Wi-Fi generation. Older b/g/n clients slow 2.4 GHz for everyone.", stack=True, decimals=0), 8, 10)
    L.add(timeseries("Clients per WLAN", [target(f"sum by (wlan) (dismist_wlan_clients{R}) {W}", "{{name}} {{wlan}}")],
                     "Clients per WLAN over the selected APs.", stack=True, decimals=0), 8, 10)
    L.add(timeseries("Weakest client", [target(f'min by (site, ap, name, radio, band) (dismist_radio_client_rssi_dbm{r(m("stat", "=", "min"))} {N})', "{{name}} {{ap}} {{radio}}")],
                     "Signal of the weakest client on each access radio. Below -75 dBm that client is slow and slows the radio.",
                     unit="dBm", min0=False, th=thresholds(CRIT, (-75, WARN), (-67, GOOD))), 12, 8)
    L.add(timeseries("Client throughput", [
        target(f'sum by (site, ap, name) (dismist_radio_client_throughput_bits_per_second{r(m("direction", "=", "tx"))} {N})', "to clients {{name}} {{ap}}", "A"),
        target(f'-sum by (site, ap, name) (dismist_radio_client_throughput_bits_per_second{r(m("direction", "=", "rx"))} {N})', "from clients {{name}} {{ap}}", "B"),
    ], "Current client throughput the AP reports, to clients above the line and from clients below (units unverified).",
        unit="bps", min0=False, overrides=[color_override("^to .*", BLUE), color_override("^from .*", ORANGE)]), 12, 8)

    L.row("Errors and activity")
    L.add(timeseries("Retries", [target(f"sum by (site, ap, name, radio, band) (rate(dismist_radio_tx_retries_total{RA}[$__rate_interval]) {N})", "{{name}} {{ap}} {{radio}}")],
                     "Retransmissions per second per access radio: weak clients and interference raise it.", unit="pps"), 8, 8)
    L.add(timeseries("PHY errors", refs(
        (f"sum by (site, ap, name, radio) (rate(dismist_radio_rx_glitches_total{RA}[$__rate_interval]) {N})", "glitches {{name}} {{ap}} {{radio}}"),
        (f"sum by (site, ap, name, radio) (rate(dismist_radio_rx_bad_fcs_total{RA}[$__rate_interval]) {N})", "bad checksum {{name}} {{ap}} {{radio}}")),
        "Energy the radio could not decode, and frames with bad checksums, per second.", unit="pps"), 8, 8)
    L.add(timeseries("Wrong passphrase", [target(f"sum by (wlan) (increase(dismist_wlan_eapol_invalid_mic_total{R}[1h])) {W}", "{{name}} {{wlan}}")],
                     "WPA handshakes that failed the passphrase check per hour: a device with an old passphrase, or guessing.",
                     bars=True, decimals=0), 8, 8)
    L.add(timeseries("Probe requests", [target(f"sum by (site, ap, name, radio, band) (rate(dismist_radio_probe_requests_total{RA}[$__rate_interval]) {N})", "{{name}} {{ap}} {{radio}}")],
                     "Probe requests heard per second: a rough measure of devices nearby.", unit="reqps"), 12, 8)
    L.add(timeseries("Multicast", [target(f"sum by (wlan) (rate(dismist_wlan_tx_multicast_bytes_total{R}[$__rate_interval]) * 8) {W}", "{{name}} {{wlan}}")],
                     "Multicast sent per WLAN. Multicast goes at the lowest rate and costs airtime.", unit="bps"), 12, 8)

    L.row("Per-client (with metrics.clients=per-client)", collapsed=True)
    C = r()
    top = (f"topk(10, sum by (site, ap, client) (rate(dismist_client_receive_bytes_total{C}[$__rate_interval]))"
           f" + sum by (site, ap, client) (rate(dismist_client_transmit_bytes_total{C}[$__rate_interval])))")
    L.add(timeseries("Top clients", [target(f"{top} * 8", "{{client}}")],
                     "The ten busiest clients (bytes the AP received from and sent to them). Only with per-client metrics, which export client MACs.",
                     unit="bps"), 12, 8)
    by = "site, ap, client"
    L.add(table("Clients", [
        target(f"max by ({by}, radio, wlan, protocol, name) (dismist_client_info{C})", ref="A", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_rssi_dbm{C})", ref="B", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_snr_db{C})", ref="C", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_tx_bitrate_bps{C})", ref="D", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_rx_bitrate_bps{C})", ref="E", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_tx_retries_total{C})", ref="F", instant=True, fmt="table"),
        target(f"max by ({by}) (dismist_client_connected_seconds{C})", ref="G", instant=True, fmt="table"),
        target("max(dismist_metrics_clients_omitted{" + NS + "})", ref="H", instant=True, fmt="table"),
    ], "Associated clients (at most metrics.clientLimit, in MAC order). Link rates are unverified.",
        ["name", "client", "ap", "radio", "wlan", "protocol", "Value #B", "Value #C", "Value #D", "Value #E", "Value #F", "Value #G"],
        {"name": "Host", "client": "MAC", "ap": "AP ID", "radio": "Radio", "wlan": "Network ID", "protocol": "Protocol",
         "Value #B": "RSSI", "Value #C": "SNR", "Value #D": "Tx rate", "Value #E": "Rx rate", "Value #F": "Retries", "Value #G": "Connected"},
        [colored("RSSI", thresholds(CRIT, (-75, WARN), (-67, BLUE), (-60, GOOD)), unit="dBm"),
         colored("SNR", thresholds(CRIT, (15, WARN), (25, GOOD)), unit="dB"),
         column("Tx rate", ("unit", "bps")), column("Rx rate", ("unit", "bps")), column("Connected", ("unit", "dtdurations"))],
        hide=("site", "Value #A", "Value #H")), 12, 8)
    L.add(timeseries("Client signal", refs(
        (f'max by (client) (dismist_client_rssi_dbm{r(m("client", "=~", "$client"))})', "RSSI {{client}}"),
        (f'max by (client) (dismist_client_snr_db{r(m("client", "=~", "$client"))})', "SNR {{client}}")),
        "RSSI (dBm) and SNR (dB) of the chosen clients.", min0=False), 24, 8)
    templating = variables(ap="multi")
    templating.append(query_var("client", "Client", f"label_values(dismist_client_info{{{NS}, {SITE}, ap=~\"$ap\"}}, client)",
                                multi=True, include_all=True, all_value=".*", current=ALL))
    links = [link("Overview", "dismist-overview", "All access points"), link("Access point", "dismist-ap", "Per-AP dashboard")]
    return dashboard("dismist-rf", "Dismist / Clients & RF",
                     "Channel plan, utilisation and interference, and client signal and protocol mix over the selected access points.",
                     L.panels, templating, links)


DASHBOARDS = (("overview.json", overview), ("access-point.json", access_point), ("clients-rf.json", clients_rf))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__.strip().splitlines()[2])
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for name, build in DASHBOARDS:
        with open(os.path.join(out, name), "w") as f:
            json.dump(build(), f, indent=1, sort_keys=True, ensure_ascii=False)
            f.write("\n")
        print("wrote", os.path.join(out, name))


if __name__ == "__main__":
    main()
