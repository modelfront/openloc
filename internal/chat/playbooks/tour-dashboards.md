---
name: tour-dashboards
title: Dashboards tour
description: "Dashboards built in the chat: ask the assistant for the charts you need and get them in a minute."
roles: [owner, admin, project_manager]
tour:
  - anchor: chat.example.dashboard
    title: Ask for a dashboard
    body: "Describe the numbers you need in plain words. The assistant queries the metrics, creates the charts and sets the filters."
    say: "A new report is one request away. In this example, a manager describes a dashboard in plain words, and the assistant queries the numbers, creates seven charts and adds the filters."
  - anchor: dashboards.grid
    page: dashboards
    title: The result
    body: "What the chat built is a normal dashboard. Organization dashboards are for the whole organization; personal ones are yours."
    say: "And here is the result, next to the other dashboards. What the chat built is a normal dashboard, not a picture. Organization dashboards are for the whole organization, and personal ones are only yours. Let's open it."
  - anchor: dashboard.charts
    pick: latest_dashboard
    title: Real charts, live data
    body: "Every chart has its own metric, split and filter: words per week, AI review results by language, editing effort in German, QA issues for one customer."
    say: "Each card is a saved chart on live data, and each can ask something different: words per week, what A I review approved per language, editing effort in German only, or Q A issues for one customer. If you need a change, just ask again."
  - anchor: dashboard.filters
    pick: latest_dashboard
    title: Filters for every chart
    body: "The dates, customer and language filters apply to every chart on the dashboard."
    say: "The filters the assistant added sit above the charts. The dates, the customer and the target language apply to every chart at once, so one dashboard answers many questions, for example the last thirty days for a single customer."
  - anchor: chart_builder.metrics
    pick: latest_dashboard
    title: Or build it by hand
    body: "New chart opens this list of metrics: volume, MT and AI engine use, AI review results, editing time and effort, QA issues and AI tokens."
    say: "If you prefer clicking, New chart opens this builder. On the left is every metric a chart can show: volume, machine translation and A I engine use, A I review results, editing time and effort, Q A issues, and A I token spend."
  - anchor: chart_builder.options
    pick: latest_dashboard
    title: Split and filter it
    body: "Choose the chart type, what the axis shows, a breakdown by language, customer, linguist or engine, and the filters on the card."
    say: "Pick a metric, and its options appear. Choose the chart type, what the axis shows, and a breakdown by language, customer, linguist or engine. Then choose the filters this chart offers. The chat and this builder make the same charts."
  - anchor: dashboard_chart.chart
    pick: dashboard_chart
    title: Every number explained
    body: "One chart on its own page, with the definition of its measure spelled out."
    say: "Here is one chart on its own page. The definition of its measure is spelled out above the chart, so a colleague who opens it from a link knows exactly what the numbers mean, and where they come from."
  - anchor: dashboard_chart.actions
    pick: dashboard_chart
    title: Download and share
    body: "Download the chart, share it by link, or edit it."
    say: "Finally, download the chart, share it by link, or edit it with these buttons. That is dashboards here: the report you need, in the minute you need it. Thanks for following along!"

---
Use this when a manager asks about dashboards, charts, metrics pages or sharing a chart.

1. Run the saved tour: `start_tour` with `playbook: "tour-dashboards"`. The steps inside a dashboard are left out when the organization has no shared dashboard with charts.
2. Show real data if asked: `openloc_list_dashboards`, `openloc_get_dashboard`, or answer a question with `openloc_query_metrics` and `render_chart`.
3. Offer to add a chart in write mode (`openloc_create_chart`) — say which dashboard and what it measures first. Only owners and admins change a shared dashboard.
