---
title: "Templates"
summary: "Save a project setup as a template and start new projects from it."
order: 80
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-17
sources:
  - web/src/pages/TemplateList.tsx
  - web/src/pages/TemplateDetail.tsx
  - web/src/pages/ProjectCreate.tsx
  - web/src/pages/ProjectDetail.tsx
  - web/src/hooks/useProjectCreateSettings.ts
  - web/src/components/project/TemplatePickerDialog.tsx
  - web/src/components/ProjectSettingsTab.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/components/workflow/WorkflowTab.tsx
  - web/src/components/workflow/WorkflowPicker.tsx
  - web/src/components/workflow/WorkflowModeToggle.tsx
  - web/src/components/workflow/RawMTPane.tsx
  - web/src/components/workflow/WorkflowStepChips.tsx
  - web/src/components/project/ProjectCreateWorkflowCard.tsx
  - web/src/components/workflow/WorkflowDeadlines.tsx
  - internal/template/**
  - internal/project/create.go
related:
  - guides/projects-and-files
  - guides/translation-memory
  - guides/machine-translation
  - guides/terminology
  - guides/quality-assurance
  - guides/workflows-and-tasks
screenshots:
  - project-create.png
  - project-create-template-picker.png
---
A template is a saved project setup without files or languages of its own. Instead of choosing memories, engines, term bases, QA checks and a workflow for every new project, you set them once in a template and pick the template when you create the project. Owners, admins and project managers can create, edit and delete templates.

## What a template carries

A template page has two tabs, **Settings** and **Workflow**. The **Project settings** block on the Settings tab starts with **Project name**, **Source language**, **Target languages** and the template's labels. Together the tabs hold:

| Block | What is saved |
| --- | --- |
| **Project name**, **Source language**, **Target languages** and the labels | What a project made from the template starts with. The project name starts as the template's name; leave any other field empty and the new project's field starts empty too. With no languages set, the New project page suggests them from the template's memories |
| **Project settings** | **Complete the project when every task is done**, [**Strings contain HTML**](/docs/reference/settings) and the **QA Profile** |
| **Translation memory** | **Apply translation memory on start or upload**, then either a **TM profile** to follow, or the template's own list of memories with their read/write modes, order and penalties, plus the match scoring percentages and **TM lookup order** |
| **Machine translation** | **Fill in machine translation on start or upload** and the **MT router**. A warning beside the router lists the template's target languages it has no engine for; see [Choose the router for a project or template](/docs/guides/machine-translation#choose-the-router-for-a-project-or-template) |
| **Terminology** | The term bases per language pair, with mode and priority |
| **Workflow** | **Workflow** or **Raw MT**: the library workflow that the template follows and the copy of its steps, or the built-in **Pre-translation** step |

A template holds no dates. The project deadline and the steps' deadlines belong to each project, and are set on the **New project** page or the project's **Workflow** tab.

## Create a template

1. Open **Templates** in the left menu and click **New template**.
2. Enter a **Name** and click **Create**. The new template starts with both pre-translation checkboxes on and two steps, Translation → Review.
3. Click the template to open it.
4. On the **Settings** tab, fill in **Project name**, the languages and the labels at the top of the **Project settings** block if projects made from this template should start with them. Fill in the rest of each block exactly as you would in a project. Changes save as you make them. To rename the template, click its name in the breadcrumb at the top.
5. On the **Workflow** tab, pick the workflow the template follows. See the next section.

## Set the template's workflow

The **Workflow** tab has one card, headed by a **Workflow** / **Raw MT** toggle. **Workflow** follows a library workflow:

1. Open the **Workflow** dropdown. It lists the workflows in your library, each with its steps; type to search.
2. Pick one and confirm with **Use workflow**. The template gets a copy of its steps, listed read-only under the field. Projects already created from the template are not affected.

Until you pick one, the steps the template starts with sit under an empty field. Picking a different workflow replaces the steps, and editing a workflow in the library never changes a template that already copied it. Workflows and the steps they are built from are made on the **Workflows** page; see [Workflows and tasks](/docs/guides/workflows-and-tasks).

The steps read down a rail: a numbered dot, the step's name, its type icon, the AI router (a link to it on the **AI** page) and who picks the step up, or **Automated**. When **Fill in machine translation on start or upload** is on and an **MT router** is chosen, an unnumbered **Pre-translation** row heads the rail with the router as a link. On a template, that link opens the **Settings** tab and highlights the **Machine translation** card, where the router is picked.

For projects that need machine translation and nothing more, choose **Raw MT** and confirm with **Use Raw MT**. The template's steps become the built-in **Pre-translation** step, and the card shows that row alone, with the MT router as a link (**No MT router yet** until one is chosen) and no dropdown. **Fill in machine translation on start or upload** on the **Settings** tab stays on and locked. Choose **Workflow** and confirm with **Use workflow** to go back to the library workflow the template had before, or else the first one in the library. See [Pre-translate only, with no linguist](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist).

## Start a project from a template

1. Open **Projects** and click **New project**. The page shows only two buttons, **From template** and **From scratch**. The form appears once you choose.
2. Choose **From template**. In the **Choose a template** dialog, click a template. Each row shows its name, the workflow it follows (or **Custom workflow**) and when it was created. Type in **Search templates** to narrow the list by name; more templates load as you scroll.
3. The form opens filled from the template. The two options give way to a line at the top: **Using template** *Name*. **Project name**, the languages and the labels come from the template. If the template has no languages, they are suggested from its memories. Change the name, check the languages and other details, then change what you like in the blocks below. A template that brings memories, a TM profile or an MT router settles where they come from, so **Apply translation memory on start or upload** and **Fill in machine translation on start or upload** can be changed straight away; without that answer each switch stays on and locked until you choose a source. See [Create a project](/docs/guides/projects-and-files#create-a-project).
4. Click **Create project**.

![The Choose a template dialog, one row per template](assets/project-create-template-picker.png)

To pick another template, change something so the form saves a draft, then click the red **Discard** button at the top right. It asks you to confirm, then deletes the draft — together with any files you had already uploaded into it — and shows the two options again. **Discard** works the same after **From scratch**. See [Create a project](/docs/guides/projects-and-files#create-a-project).

![The New project form after From scratch](assets/project-create.png)

The **Workflow** card shows the template's workflow selected, with its steps; a Raw MT template arrives with **Raw MT** selected. If the template follows no workflow, the steps carried over from it are listed under an empty field. To give this one project a different workflow, choose **Workflow** if the card is on **Raw MT**, then pick it in the **Workflow** dropdown — its steps replace the template's. The toggle switches at once here, with no confirmation. The template itself is unchanged.

Each step's deadline, at the right of its row, starts empty. Fill it in to say when the step is due, and use the globe beside it for a target language due on a different date — or leave them and schedule the project later on its **Workflow** tab. See [Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines).

## Start a project from another project

Owners, admins and project managers can copy a project's setup without saving a template first. Open the project and click **Use as template** at the top of the page, next to the language pair. The New project page opens with the line **Settings copied from project** *Name* at the top; the name, domain, customer, languages, settings and workflow steps are copied, files, dates and status are not. Once you have changed something, the red **Discard** button at the top right deletes the draft — with any files uploaded into it — after asking you to confirm, and shows the two options again. The **Duplicate** icon on the **Projects** page does the same as **Use as template**.

## Understand what "applied" means

![A template and a library workflow are copied into the project once, so editing them later changes nothing. A TM profile is different: it stays linked, so edits reach every project that follows it.](assets/template-copy-vs-profile.svg)

Applying a template is a one-time copy at creation. Afterwards the project and the template are independent:

- Changing the template later does not change projects made from it.
- Changing the project does not change the template.
- The project's **Settings** tab shows **Based on template "Name"** at the top, so you can see where its setup came from. Deleting the template only removes this note.

The workflow follows the same rule. The project gets its own copy of the steps, and its **Workflow** tab shows the library workflow they came from. Editing that workflow in the library does not touch the project.

There is one exception. If the template follows a **TM profile**, the new project follows the same profile. A profile is a live link, not a copy: add a memory to the profile and every project and template following it sees it at once. If you want the project to have its own copy instead, use **Detach and customise** in the project's **Translation memory** card. See [Share a setup with TM profiles](/docs/guides/translation-memory#share-a-setup-with-tm-profiles).

## Save a new project's setup as a template

When the New project form is set up the way you like, save it as a template instead of, or before, creating the project:

1. On the **New project** page, fill in the form. Starting **From template** or with **Use as template** from another project works too.
2. Click **Save as template**, next to **Create project**.
3. The **Template name** starts as the project name. Change it if you like and click **Save template**.

Everything in the table above is saved: the project name, languages, domain and customer, the match scoring, the completion setting, the TM profile link, the workflow steps and the library workflow they follow. Files and deadlines are not. The form stays as it is, so you can go on and click **Create project**, or leave. To turn an existing project's setup into a template, open the project, click **Use as template**, and then **Save as template** on the New project page.

## Duplicate a template

Hover over a template in the list and click the copy icon (**Duplicate template**). A full copy named **Copy of** the original opens right away, ready for renaming and editing. Use this to make a variant, for example the same setup with a stricter QA profile.

## Delete a template

Hover over the template and click the trash icon (**Delete template**), then confirm. The template and all its TM, MT, terminology and workflow settings are removed for good — there is no undo and no trash to restore it from. Projects created from it keep their settings.

## Templates and other shared setups

Templates are one of four ways to share configuration. Pick the one that fits:

| You want to | Use |
| --- | --- |
| Start new projects with a full setup, then adjust each one freely | A template |
| Keep the memory list and match scoring the same across many projects, live | A [TM profile](/docs/guides/translation-memory#share-a-setup-with-tm-profiles) |
| Reuse one set of quality checks | A [QA profile](/docs/guides/quality-assurance) |
| Reuse one ordered list of steps | A [workflow](/docs/guides/workflows-and-tasks) from the **Workflows** library |

A template can point at a TM profile, a QA profile and a workflow, so the four work together.
