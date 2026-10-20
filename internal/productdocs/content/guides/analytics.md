---
title: "Analytics and dashboards"
summary: "Read the word-count analysis and productivity reports, and build dashboards."
order: 100
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/DashboardsPage.tsx
  - web/src/pages/PinnedDashboardPage.tsx
  - web/src/pages/DashboardPage.tsx
  - web/src/pages/ChartPage.tsx
  - web/src/pages/SharedChartPage.tsx
  - web/src/components/charts/**
  - web/src/components/AnalysisBreakdown.tsx
  - web/src/components/NewAnalysisModal.tsx
  - web/src/components/ProjectAnalysesTab.tsx
  - web/src/components/AnalysisExportMenu.tsx
  - internal/analysis/export.go
  - web/src/components/project/UploadConfirmDialog.tsx
  - web/src/components/org/AnalysisWeights.tsx
  - web/src/pages/org/OrgSettingsPage.tsx
  - internal/analytics/**
  - internal/analysis/**
  - internal/dashboard/**
  - internal/dashboarddefaults/**
  - internal/metrics/**
limitations:
  - analysis-export-no-ui
related:
  - concepts/analysis-bands
  - guides/ai-assistant
  - guides/projects-and-files
  - reference/settings
screenshots:
  - project-analysis.png
---
An analysis counts how much of a project is already covered by your translation memory (TM) and machine translation (MT), and how much is new work. You use it to plan effort and check what pre-translation did for you. Dashboards hold live charts about words, time, quality and MT usage, and you build them yourself or with the AI assistant. For what each match band means, see [Analysis bands](/docs/concepts/analysis-bands).

## Run a new analysis

An analysis is a snapshot. It reads the current translation of every segment in the jobs you pick and sorts them into match bands.

1. Open the project and click the **Analyses** tab.
2. Click **New analysis**.
3. Type a **Name (optional)**. If you leave it empty, the dialog shows the name it will use under **Saved as**, built from the file and language names.
4. Under **Jobs**, tick the jobs to include. All jobs are ticked by default. Use **Select all** or **Clear** to change many at once.
5. Click **Run analysis**. The button shows how many jobs are selected.

The new row appears in the **Saved analyses** list as **Pending…**. The list refreshes on its own. When the row shows a job count, the analysis is complete and you can click it. If something went wrong, the row shows the reason in red under its name.

Every job in the analysis must have finished its pre-translation first. If not, the dialog shows a message that names the jobs that are not ready and asks you to run Apply TM/MT first. See [Projects and files](/docs/guides/projects-and-files).

## Let uploads analyze the project for you

The upload dialog has a checkbox, **Auto-analyze project files**, which is on by default. When it is on, OpenLoc runs one analysis of the whole project after the pre-translation of the uploaded files has finished; several files uploaded together produce one analysis. These are named **Auto analysis** followed by the date and time. Untick the box if you only want analyses that you start yourself.

## Read the results

Click a completed row in the **Saved analyses** list, or the arrow at its start. The details open directly under that row; click it again to close them. One analysis is open at a time, so opening another closes the first. A row that is pending, running or failed has a greyed arrow and does not open.

![A project analysis with its breakdown tables](assets/project-analysis.png)

The first line shows the number of jobs and when the analysis was created and completed. The switch on the right changes the unit: **Segments**, **Words** or **Characters**. Words is the default because most pricing is per word.

Below, there is one table per target language, headed with its language pair, for example **EN → DE**. Each row is one file, and the columns are the match bands — **Repetitions**, **New**, **65–84%**, **85–99%**, **100%**, **101%** and a **Total** for the row. See [The bands](/docs/concepts/analysis-bands#the-bands) for what lands in each.

A long file name is cut short; hover over it to see it in full. To make the **File** column wider, drag the right edge of its header, and double-click the edge to put it back. The width applies to every language table and is kept in this browser; a table wider than the page scrolls sideways.

A small **auto-approved** number under a value shows how much of that band the quality check approved without human review. The **Total** row sums each language; there is no grand total across languages. A job deleted after the run keeps its row, marked **(deleted)**.

The AI assistant can report the same numbers and group them the way you need.

## Download an analysis

1. In the **Saved analyses** list, click **Download** at the end of a completed row.
2. Choose a format:
   - **Excel (XLSX)** — one row per file and target language, with an empty row between target languages, and an **About** sheet with the analysis name, source language and dates.
   - **CSV** — the same table without the empty rows, so it sorts and imports cleanly.

Both files have every unit — segments, words and characters — for each band, each with its auto-approved count. The file is named after the analysis and the download date.

## Delete an analysis

1. In the **Saved analyses** list, click **Delete** at the end of the row.
2. Confirm in the dialog.

The snapshot is removed for good. To get fresh numbers later, run a new analysis.

## Set analysis weights

Owners and admins define how much effort each band counts for, on the **Settings** tab of the **Organization** page. To open it, choose **Organization settings** in your avatar menu at the top right.

**Analysis weights** hold one percentage per band. 0% means no effort (free), 100% means full effort. The defaults follow industry practice: exact matches at 0%, new words at 100%.

## Ask the assistant for numbers

The built-in AI assistant answers "how much" and "how many" questions across the whole organization: source volume, post-editing volume over time, translation activity by type (TM, MT, post-edit, human), MT engine usage, AI engine usage, AI token usage, auto-approved words, recorded post-editing time, post-editing effort, and QA issues. You can group by target language, user, project, customer, MT engine, AI engine, workflow step, origin, status, and by day, week or month. Work done outside a workflow step, such as pre-translation, has an empty workflow step.

**Source volume** is everything that entered translation: each segment once per target language, on the day it was added, with its source words. Group it by origin to see how it was first filled (TM, MT, post-edit, file import, or nothing automatic), or by status to see where it stands now. **Post-editing volume** counts every segment a person confirmed, whether or not they changed the text, plus text somebody typed and left unconfirmed when their step was completed, credited to the person who typed it (see [a segment's history](/docs/guides/cat-editor#see-a-segments-history)). **Post-editing effort** measures rewriting only, so a confirm that changed nothing does not enter its average. **Post-editing time** is the time a segment was the active one in the editor, added up over every visit and recorded when someone confirms it with Ctrl+Enter; **Confirm** in the selection bar, **Approve all and complete** and draft saves record none. **Auto-approved** counts each segment once, on the day quality prediction approved it; group it by segment type to see whether the approved text came from MT, TM or a file import. **QA issues** counts open findings; ignored ones are left out.

Open the assistant with the round button at the bottom right and ask, for example: "How many words did we process per month this year, by target language?" It queries the data and can draw the result as a chart in the chat. Every answer states how the measure is defined. See [The AI assistant](/docs/guides/ai-assistant).

## Build a dashboard

A dashboard is a page of saved charts. The charts are live: each one re-runs its query when you open the page. Dashboards live on two tabs: **Organization dashboards**, which the whole organization sees, and **My dashboards**, which only you see. A third tab, **Pinned**, collects what you pin from either (see [Pin dashboards and charts](#pin-dashboards-and-charts)).

1. Open **Dashboards** in the left menu.
2. Click **New dashboard**, type a **Name** and click **Create**. Owners and admins also choose who sees it under **Visible to**. The new dashboard is empty and opens straight away.
3. Click **New chart** and pick a **Metric**. The **Title** fills in from it.
4. Choose a **Chart type** and what goes on the **X axis** (for a pie, **One slice per**). **Breakdown by** splits the chart into one series per value, for example one line per target language; it starts at **No breakdown**. Tick the **Units** to show and, under **Filters on this card**, any dropdowns the chart should carry itself.
5. Click **Create chart**.

Instead of the form, click **Ask the assistant** in the **New chart** dialog: the chat opens with a prompt for a chart this dashboard does not have yet, ready to edit and send. You can also ask the assistant directly, for example "Chart MT usage by engine and save it to a dashboard called Vendors". Switch it to **Allow write** mode when it asks: saving a chart is a change, so read-only mode cannot do it. A dashboard the assistant creates goes under **My dashboards** unless you ask for it to be shared.

Each dashboard card previews its first four charts. Open a dashboard by clicking its card. Each chart card shows its title; hover over the title to see what the chart counts, and click it to open the chart on its own page. When the dashboard has a **Date range** filter, it sets the period for every chart. Use the breadcrumb at the top (**Dashboards** › **Organization dashboards**, **My dashboards** or **Pinned**) to go back. A chart with no data shows **No data in this period yet.**; a chart whose definition is no longer valid shows an error in its card instead of breaking the page.

A chart with several series or slices, and any chart with a **Breakdown by**, shows a legend under it. Click an entry to show only that series or slice; the others stay in the legend, greyed out and struck through. Click more entries to add them, or a shown one to take it out. Taking out the last one shows everything again. Reloading the page shows everything again. The biggest entry comes first, and a pie shows its seven biggest slices and one "Other" for the rest. Hover over an entry to highlight it in the chart and see its number in a tooltip: a slice's value, or a series' total for the period, with its unit and its share of what is shown. Averages, such as post-editing effort, show no total. Hovering the chart itself highlights the band, bar or slice under the pointer the same way. The chart's tooltip lists every series, or every slice of a pie with its share in percent of the slices still shown, sets the one under the pointer in bold, and ends with their total when they add up. This works the same on a chart's own page, on a shared chart link and in the AI assistant.

## Change a dashboard or a chart

You can change your own dashboards under **My dashboards** and the charts on them. On the **Organization dashboards** tab, owners and admins can. On a dashboard you cannot change, **New chart** does not appear.

- **Rename a dashboard or pick its filters.** Click the pencil next to the dashboard's name and choose **Rename**, or tick an entry under **Filters** to add that dropdown to the dashboard's filter row. Unticking one asks first: the dropdown goes away and the charts stay fixed at the value it had.
- **Edit a chart.** Hover over the chart card and click the pencil. The same form opens with the chart's choices in place; change them and click **Save chart**.
- **Reorder.** Drag a dashboard card, or a chart card, by the grip at its top edge. With the keyboard: Space, the arrow keys, then Space.
- **Move or copy a dashboard.** Hover over its card and click the folder icon: **Move to Organization dashboards** / **Move to My dashboards**, or **Copy to Organization dashboards** / **Copy to My dashboards**. Anyone who can see a shared dashboard can copy it into **My dashboards**.
- **Move or copy a chart.** Hover over the chart card, click the folder icon, and pick a dashboard under **Move to** or **Copy to**. Copying needs only the right to change the dashboard it goes to, so you can copy a shared chart into your own dashboard. If that dashboard's own filters would take over a filter the chart has, you are asked before it moves.

## Pin dashboards and charts

Pinning builds a personal view of the dashboards and charts you check most. Pins are yours alone.

- **Pin.** Click the pin before the name of a dashboard or chart, on its card or its own page. It fills once pinned. Pinning a dashboard pins all its charts, including ones added later. Pinning one chart adds it to a pinned dashboard with the same name as the chart's dashboard, created if missing. Unpinning one chart leaves the dashboard's other charts pinned, and the dashboard's own pin then shows as unpinned.
- **Open Pinned.** **Pinned** is the last tab on **Dashboards**, greyed out while nothing is pinned. **Dashboards** opens on **Pinned** when you have pins, otherwise on **Organization dashboards**. Clicking a pinned dashboard stays in **Pinned**.
- **Arrange it.** In **Pinned** you can drag pinned dashboards and charts to reorder them, move a chart to another pinned dashboard, rename a pinned dashboard, and add or remove its filters. Removing a filter there does not freeze a value. None of this changes the original dashboards, and nothing is created in **Pinned**: there is no **New dashboard** or **New chart**, and chart titles are not editable.
- **Open the original.** Opening a chart shows its original chart page. Edits there change the original chart, if you may edit it.

A chart you can no longer see leaves **Pinned**.

## Download or share a chart

The download icon on a chart card, and on a chart's own page, saves the chart as a spreadsheet, with the rows the chart shows under the current filters.

On a chart's own page, the share icon hands the chart to someone else:

- If you can change the chart, it creates a link. Anyone signed in to your organization can open it, even if they cannot normally see the dashboard it lives on, and the link keeps the filters you had selected. **Stop sharing** in the same dialog turns the link off.
- Otherwise it copies the address of the page, which opens for people who can already see the chart.

A shared link opens the chart on a page of its own, with its **Date range**, **Unit** and **Granularity** filters and the download icon.

## Reset the organization dashboards

Owners and admins can replace the **Organization dashboards** tab with the built-in dashboards. On that tab, click the reset icon at the end of the tab row, then **Delete and reset**. The dialog says how many people would lose pinned charts. This deletes every organization dashboard, including ones your team created, and share links to charts on them stop working. Dashboards under **My dashboards** are not affected.

## Delete a dashboard or a chart

Hover over the dashboard card (or the chart card), click the trash icon that appears, and confirm. You can delete your own dashboards under **My dashboards** and the charts on them. On the **Organization dashboards** tab, only owners and admins can delete. Deleting a dashboard removes all its charts. There is no undo, but you can rebuild a chart with **New chart** or by asking the assistant.
