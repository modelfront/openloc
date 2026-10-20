---
name: demo-overview
title: Product overview demo
description: "The whole product in a few minutes — the basics, then AI review, prompts, dashboards, the assistant and guided tours."
roles: [owner, admin, project_manager]
tour:
  - anchor: projects.list
    page: projects
    title: Your projects
    body: "Every translation job lives in a project: files, languages, customer, status and deadline."
    say: "Welcome! Let me show you around. Everything starts with projects. Each row in this list is one project, with the files you need translated, the languages you want, the customer and the deadline. Now let's open the work itself."
  - anchor: editor.segments
    pick: active_job
    title: The editor
    body: "Source on the left, translation on the right. Memory, machine translation and AI review may already have filled it."
    say: "This is the translation editor, with the original text on the left and the translation on the right. Translation memory, machine translation and A I review may already have filled the translation, so people start from a draft, not from nothing."
  - anchor: editor.filters
    pick: active_job
    title: Filters
    body: "Filter by status or origin, or to the segments AI review did not approve."
    say: "These filters narrow the list: by status, by origin, or to the segments A I review did not approve. A reviewer can go straight to the segments that still need a person, and leave the rest alone."
  - anchor: ai_routers.list
    page: ai_routers
    title: AI review
    body: "Which AI engine reviews each target language, with a default for the rest."
    say: "A I review is set up here. A router decides which A I engine reviews each target language, with a default for all the others. The engines score translations, fix the weak ones, and only approve what they are sure about."
  - anchor: ai_prompts.list
    page: ai_prompts
    title: Open prompts
    body: "No hidden prompts: read and change every instruction, add rules per language, and choose the context each segment sends."
    say: "Every prompt is open: you read and change the instructions, add rules for single languages, choose the context each segment sends, and see the output schema. The prompts tour has the details."
  - anchor: dashboards.grid
    page: dashboards
    title: Dashboards
    body: "Volume, quality, speed and AI results as charts. Ask the assistant for a new dashboard."
    say: "The dashboards show volume, quality, speed and A I results across all your projects. Need a new report? Ask the assistant, and it builds the dashboard for you in a minute."
  - anchor: chat.launcher
    title: The assistant
    body: "Create and run projects, find answers, build dashboards and tours, just by asking. It also works from Claude and other tools over MCP."
    say: "Down in the corner is the A I assistant. It can create and run projects, find answers in your memories and the docs, and build dashboards and tours, also from Claude over M C P. More tours are under Learn, in the account menu. That is the overview. Thanks for watching, and enjoy exploring!"
---
Use this when the user asks for a demo, a tour, or "show me around" and is a manager.

1. Say in one sentence what you will show: the basics, then AI review, prompts, dashboards and the assistant.
2. Run the saved tour: `start_tour` with `playbook: "demo-overview"`. If the organization has no projects, suggest opening the Playground, where every tour has sample data.
3. After the tour, offer the next step in one line: a feature tour (`tour-ai-prompts`, `tour-assistant-and-mcp`, `tour-dashboards`).

Keep narration short — the tour popovers already carry the explanation.
