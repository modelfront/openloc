---
name: weekly-ops-report
description: Produce a weekly translation-operations report from OpenLoc — volume, MT usage, QA and overdue work — with charts. Use when the user asks for an ops report, weekly summary, or "how did translation go this week/month".
---

# Weekly translation ops report

Build the report from OpenLoc MCP tools, in this order. Query everything
first, then present; keep each section to 2-3 sentences plus the data.

1. **Volume** — `openloc_query_metrics` with `measure=words_processed`,
   `dimensions=["week"]` over the requested period (default last 30 days).
   Note the definition the tool returns: words count once per translation.
2. **Automation split** — `measure=segments_by_origin`, `dimensions=["kind"]`.
   Report the MT / TM / human share of segments.
3. **MT engines** — `measure=mt_engine_usage`, `dimensions=["mt_engine"]`.
4. **Quality** — `measure=qa_issues`, `dimensions=["severity"]`, last 30 days.
5. **Overdue work** — `openloc_list_jobs` with `overdue_only=true` and no
   project_id (never per-project iteration); while `has_more` is true, call
   again with `cursor` set to `next_cursor`.

Present: a one-paragraph executive summary, then the sections. Where the user
wants visuals and charts are supported, chart the weekly volume (area) and
engine usage (bar). Flag anything anomalous (zero volume weeks, error-severity
QA spikes, overdue jobs) explicitly.
