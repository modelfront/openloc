---
name: project-setup
description: Create a fully configured OpenLoc translation project conversationally. Use when the user wants to start a new translation project, onboard a new client, or set up a language pair.
---

# Set up a translation project

Gather before creating — never guess:

1. Project name, source language, target language(s). Ask if missing.
2. Client: `openloc_list_customers` first; reuse an existing customer or
   create one with `openloc_create_customer` (confirm the name).
3. Template: check existing templates via the project listing context; prefer
   `template_id` when one fits — it copies TM/MT/terminology/workflow/QA
   settings.

Then:

4. Restate the plan in one line and get an explicit yes.
5. `openloc_create_project` with the confirmed arguments.
6. If the pair needs a glossary and none exists, offer
   `openloc_create_term_base` for the language pair.
7. Finish by telling the user the project is ready for file upload in the
   OpenLoc app — files cannot be uploaded through chat.

All writes require the connection to have write scope; if calls are refused,
tell the user to reconnect with read-and-write access.
