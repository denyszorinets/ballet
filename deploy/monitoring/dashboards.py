"""Generates the Grafana dashboards of deploy/monitoring (run: python3 dashboards.py)."""
import json
import pathlib

DS = {"type": "prometheus", "uid": "prometheus"}
SEL = 'organization=~"$organization", project=~"$project"'


def variables():
    def var(name, label):
        return {"name": name, "label": label, "type": "query", "datasource": DS, "multi": True, "includeAll": True,
                "allValue": ".*", "current": {"text": "All", "value": "$__all"}, "refresh": 2, "sort": 1,
                "query": {"query": f'label_values({{__name__=~"ballet_.+", organization!=""}}, {name})',
                          "refId": "var"}}
    return {"list": [var("organization", "Organization"), var("project", "Project")]}


def panel(pid, title, kind, exprs, x, y, w=12, h=8, unit=None, legend="{{project}}", description=""):
    targets = [{"refId": chr(65 + i), "datasource": DS, "expr": e[0], "legendFormat": e[1] if len(e) > 1 else legend}
               for i, e in enumerate(exprs)]
    p = {"id": pid, "title": title, "type": kind, "datasource": DS, "targets": targets,
         "gridPos": {"x": x, "y": y, "w": w, "h": h}, "description": description,
         "fieldConfig": {"defaults": {}, "overrides": []}, "options": {}}
    if unit:
        p["fieldConfig"]["defaults"]["unit"] = unit
    if kind == "stat":
        p["options"] = {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                        "colorMode": "value", "graphMode": "none"}
    if kind == "bargauge":
        p["options"] = {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                        "orientation": "horizontal", "displayMode": "basic"}
    return p


def dashboard(uid, title, description, panels):
    return {"uid": uid, "title": title, "description": description, "tags": ["ballet"], "timezone": "browser",
            "schemaVersion": 39, "version": 1, "editable": False, "refresh": "1m",
            "time": {"from": "now-7d", "to": "now"}, "templating": variables(), "panels": panels}


delivery = dashboard("ballet-delivery", "Ballet · Delivery", "Throughput, lead time, rework and questions per project.", [
    panel(1, "Tickets done", "stat", [(f'sum(increase(ballet_flows_finished_total{{status="done", {SEL}}}[$__range])) or vector(0)', "done")], 0, 0, 6, 4),
    panel(2, "Pipelines failed", "stat", [(f'sum(increase(ballet_flows_finished_total{{status="failed", {SEL}}}[$__range])) or vector(0)', "failed")], 6, 0, 6, 4),
    panel(3, "Questions answered by the planner", "stat", [(f'(sum(increase(ballet_questions_answered_total{{by="planner", {SEL}}}[$__range])) or vector(0)) / clamp_min(sum(increase(ballet_questions_answered_total{{{SEL}}}[$__range])) or vector(0), 1)', "share")], 12, 0, 6, 4, unit="percentunit"),
    panel(4, "Open questions", "stat", [(f'sum(ballet_questions_open{{{SEL}}}) or vector(0)', "open")], 18, 0, 6, 4),
    panel(5, "Tickets done per day", "timeseries", [(f'sum by (project) (increase(ballet_flows_finished_total{{status="done", {SEL}}}[1d]))',)], 0, 4),
    panel(6, "Lead time (median, 90th percentile)", "timeseries", [
        (f'histogram_quantile(0.5, sum by (le) (increase(ballet_flow_lead_time_seconds_bucket{{status="done", {SEL}}}[1d])))', "median"),
        (f'histogram_quantile(0.9, sum by (le) (increase(ballet_flow_lead_time_seconds_bucket{{status="done", {SEL}}}[1d])))', "p90")], 12, 4, unit="s"),
    panel(7, "Rework: loops per finished ticket", "timeseries", [(f'sum by (project) (increase(ballet_flow_iterations_sum{{{SEL}}}[1d])) / sum by (project) (increase(ballet_flow_iterations_count{{{SEL}}}[1d]))',)], 0, 12),
    panel(8, "Stage outcomes", "bargauge", [(f'sum by (stage, outcome) (increase(ballet_stages_finished_total{{{SEL}}}[$__range]))', "{{stage}} {{outcome}}")], 12, 12),
    panel(9, "Pipelines in progress by state", "timeseries", [(f'sum by (status, waiting) (ballet_flows_active{{{SEL}}})', "{{status}} {{waiting}}")], 0, 20),
    panel(10, "Waits started, by reason", "timeseries", [(f'sum by (reason) (increase(ballet_flow_waits_total{{{SEL}}}[1h]))', "{{reason}}")], 12, 20),
    panel(11, "Questions raised and answered per day", "timeseries", [
        (f'sum(increase(ballet_questions_raised_total{{{SEL}}}[1d]))', "raised"),
        (f'sum by (by) (increase(ballet_questions_answered_total{{{SEL}}}[1d]))', "answered by {{by}}")], 0, 28),
    panel(12, "Time to answer (median)", "timeseries", [(f'histogram_quantile(0.5, sum by (le, by) (increase(ballet_question_answer_seconds_bucket{{{SEL}}}[1d])))', "{{by}}")], 12, 28, unit="s"),
])

usage = dashboard("ballet-llm-usage", "Ballet · LLM usage", "Tokens and requests per organization, project and model.", [
    panel(1, "Tokens in range (counted: input, output, cache writes)", "stat", [(f'sum(increase(ballet_usage_tokens_total{{type=~"input|output|cache_write", {SEL}}}[$__range])) or vector(0)', "tokens")], 0, 0, 8, 4, unit="short"),
    panel(2, "Requests in range", "stat", [(f'sum(increase(ballet_llm_requests_total{{{SEL}}}[$__range])) or vector(0)', "requests")], 8, 0, 8, 4),
    panel(3, "Refused or failed requests", "stat", [(f'sum(increase(ballet_llm_requests_total{{status!~"2..", {SEL}}}[$__range])) or vector(0)', "errors")], 16, 0, 8, 4),
    panel(4, "Tokens per hour by project", "timeseries", [(f'sum by (project) (increase(ballet_llm_tokens_total{{{SEL}}}[1h]))',)], 0, 4, unit="short"),
    panel(5, "Tokens per hour by model", "timeseries", [(f'sum by (model) (increase(ballet_llm_tokens_total{{{SEL}}}[1h]))', "{{model}}")], 12, 4, unit="short"),
    panel(6, "Tokens by type", "timeseries", [(f'sum by (token_type) (increase(ballet_llm_tokens_total{{{SEL}}}[1h]))', "{{token_type}}")], 0, 12, unit="short"),
    panel(7, "Requests by status", "timeseries", [(f'sum by (status) (increase(ballet_llm_requests_total{{{SEL}}}[1h]))', "{{status}}")], 12, 12),
    panel(8, "Tokens per organization (range)", "bargauge", [(f'sum by (organization) (increase(ballet_usage_tokens_total{{type=~"input|output|cache_write", {SEL}}}[$__range]))', "{{organization}}")], 0, 20, unit="short"),
    panel(9, "Tokens per project (range)", "bargauge", [(f'sum by (project) (increase(ballet_usage_tokens_total{{type=~"input|output|cache_write", {SEL}}}[$__range]))',)], 12, 20, unit="short"),
])

platform = dashboard("ballet-platform", "Ballet · Platform", "Services, jobs, runs and sessions.", [
    panel(1, "Services up", "stat", [('up{job=~"core|knowledge|gateway|agent"}', "{{job}}")], 0, 0, 12, 4),
    panel(2, "Dead jobs in range", "stat", [('sum(increase(ballet_jobs_total{result="dead"}[$__range])) or vector(0)', "dead")], 12, 0, 6, 4),
    panel(3, "Runs active", "stat", [(f'sum(ballet_runs_active{{{SEL}}}) or vector(0)', "active")], 18, 0, 6, 4),
    panel(4, "Jobs by kind and result", "timeseries", [('sum by (kind, result) (increase(ballet_jobs_total[1h]))', "{{kind}} {{result}}")], 0, 4),
    panel(5, "Runs by status", "timeseries", [(f'sum by (status) (ballet_runs_active{{{SEL}}})', "{{status}}")], 12, 4),
    panel(6, "Sessions finished per hour, by status", "timeseries", [(f'sum by (status) (increase(ballet_runs_finished_total{{{SEL}}}[1h]))', "{{status}}")], 0, 12),
    panel(7, "Session duration (90th percentile) by stage", "timeseries", [(f'histogram_quantile(0.9, sum by (le, stage) (increase(ballet_run_duration_seconds_bucket{{{SEL}}}[1h])))', "{{stage}}")], 12, 12, unit="s"),
    panel(8, "Planner compactions per day", "timeseries", [('sum(increase(ballet_planner_compactions_total[1d]))', "compactions")], 0, 20),
    panel(9, "Service versions", "table", [('ballet_build_info', "{{service}} {{version}}")], 12, 20),
])

out = pathlib.Path(__file__).parent / "grafana" / "dashboards"
for d in (delivery, usage, platform):
    (out / f"{d['uid'].removeprefix('ballet-')}.json").write_text(json.dumps(d, indent=2) + "\n")
print("wrote", ", ".join(sorted(p.name for p in out.glob("*.json"))))
