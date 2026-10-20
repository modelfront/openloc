---
title: "Notifications"
summary: "The bell, real-time updates, email notifications and how to mute what you do not need."
order: 110
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/components/NotificationBell.tsx
  - web/src/hooks/useNotificationStream.ts
  - web/src/pages/SettingsPage.tsx
  - web/src/components/org/NotificationRules.tsx
  - web/src/pages/org/**
  - internal/notification/**
  - internal/workflow/service.go
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/project/task-matrix/**
  - internal/issues/notify.go
related:
  - guides/workflows-and-tasks
  - reference/settings
  - reference/roles-and-permissions
  - getting-started/interface-tour
  - guides/report-an-issue
---
OpenLoc tells you when work lands on your desk, when work comes back, when a deadline is close, and when something you started fails. Workflow events create a notification in the app and, by default, an email. Failures appear only in the app.

## Read your notifications

1. Look at the bell in the top right of any page. A red badge shows how many notifications you have not read yet. Above 99 it shows **99+**.
2. Click the bell. A panel named **Notifications** opens with your ten most recent notifications. Unread ones have a light background, bold text and a dot on the right.
3. Each item shows a short title, one or two lines of detail (for example the project name or the reason for a rejection) and how long ago it happened. Detail cut off after two lines shows in full when you point at it.
4. Click an item to mark it as read. The panel closes. It also closes when you press Escape or click outside it.
5. Click **Mark all read** at the top of the panel to clear the badge in one step.

New notifications appear without a page reload. If you have several browser tabs open, the badge updates in all of them at the same time. The bell covers the organization you are working in. If you belong to several, notifications for the others show up after you switch to them.

## Know which events create a notification

Four workflow events create a notification. The workflow is the series of steps a job goes through, for example Translation then Review.

| Event | When it happens | Who receives it |
|---|---|---|
| **Task assignment** | A step is completed and the job moves to the next step. | Whoever holds that next step: the person a manager already assigned to it, otherwise its default assignee. |
| **Step rejection** | A reviewer rejects a step and sends the job back. The reason, if given, is included. | The person who did the rejected step. |
| **Workflow completion** | The last step of a job is completed. | The person who completed it. |
| **Deadline approaching** | The deadline of a job's current step is less than 24 hours away. | The person assigned to that step. |

A few details worth knowing:

- Deadline reminders come from a check that runs every hour. A job is reminded once, not every hour: a second reminder for the same step can follow 20 hours later at the earliest.
- Reminders stop once the deadline has passed. The reminder is about a deadline that is still ahead, so an overdue step is not reminded again — the red **Overdue** pill on the job is what tells you.
- Only step deadlines trigger a reminder. A project deadline does not. If a step has no deadline, nobody is reminded.
- The notification names the workflow step, the file, the target language and the project, so you can find the work from the **Tasks** page.
- A task assignment carries the comment the previous step left for you, if any (see [Complete a step](/docs/guides/workflows-and-tasks#complete-a-step)). The bell ends its detail with "Comment: …". The email quotes it under **Comment**, with the line breaks kept.

See [Workflows and tasks](/docs/guides/workflows-and-tasks) for how steps, assignees and deadlines are set.

Two failures also create a notification. They appear only under the bell: no email is sent, and neither **Notification rules** nor your own Settings can switch them off.

| Notification | When it happens | Who receives it |
|---|---|---|
| **Machine translation failed** | The machine translation step of a job's run fails. | The person who started the run and the project's creator, once each. For a run that starts on upload, the person who started it is the uploader. |
| **Job failed** | Any other part of a job's run fails. | Same as above. |

The detail line says what went wrong, for example "Machine translation failed: OpenAI is having a problem right now. Try again in a few minutes." A failure the app retries on its own notifies only when the last attempt fails.

When the support team replies to or changes the status of an issue you reported, you get an **Issue updates** notification and an email. Clicking it opens the issue. An image placed in a reply shows as "[image: name]" in the bell and email preview. See [Report an issue](/docs/guides/report-an-issue).

When the support team resolves an issue, the organization's owners, admins and project managers also get an **Issue resolved** notification, with the issue title. It appears only under the bell: no email is sent, and neither **Notification rules** nor your own Settings can switch it off. The person who reported the issue gets the **Issue updates** notification and email instead, not both.

## Get notifications by email

Every workflow event above is also sent as an email to the address shown in your personal Settings, unless you or your organization switched it off (see the next two sections). The email subject follows the same pattern as the in-app title, for example "You've been assigned to Review — brochure.docx (de)". Emails about an issue, to you as the reporter or to the support team, all use the subject "Issue #<id>: <title>", so your mail app keeps one issue's emails in one conversation. The bell keeps its own wording, "Update on your issue: …".

The email lists the project, file and language. Its button opens the job in the editor: **Open task** for an assignment, **Open job** for the other events. The **Notification settings** link at the bottom goes to your personal Settings.

In-app notifications always arrive, even when the email for that event is muted.

## Mute emails you do not want

You can stop emails for each event, one by one. This only affects your own emails. Notifications still appear under the bell.

1. Click your name in the top right and choose **Settings**.
2. Scroll to **Email notifications**.
3. Untick the events you do not want an email for: **Task assignment**, **Step rejection**, **Workflow completion**, **Deadline approaching** or **Issue updates**.
4. Each change is saved right away. You see the message "Notification preferences saved".

Tick the box again at any time to turn the email back on.

## Switch off an event for the whole organization

Owners and admins can turn off a notification type for everyone. This is a master switch: when it is off, nobody gets an in-app notification or an email for that event, whatever their personal settings say.

1. Click your name in the top right and choose **Organization settings**.
2. Open the **Settings** tab and scroll to **Notification rules**.
3. Untick the event you want to silence: **Task assignment**, **Step rejection**, **Workflow completion** or **Deadline approaching**.
4. The change is saved right away and applies to every member. If the current rules cannot be loaded, the card shows **Retry** instead of the checkboxes.

Members can still mute their own emails for the events you leave on. They cannot turn on an event you switched off here. **Issue updates** are not on this list: they concern each member's own reports.

## Stop deadline reminders for one job

Reminders follow the step deadline. A project manager, admin or owner can stop them for a single job:

1. Open the project and go to the **Tasks** tab, then open the section of the step the job is on.
2. Tick the box at the start of the job's row. Clicking the row itself opens the job in the editor.
3. Click **Set deadline** in the bar that appears, and clear the date (or move it further away). See [Select tasks and act on them](/docs/guides/projects-and-files#select-tasks-and-act-on-them).

Without a deadline in the next 24 hours, no reminder is sent.

## Troubleshooting

| Problem | What to check |
|---|---|
| The badge does not update. | The app also refreshes the count every minute. Reload the page if it still looks wrong. |
| No email arrives. | Check **Email notifications** in your Settings, then ask an admin whether the event is on under **Notification rules**. Also check the email address in your profile. |
| A reminder came for a job I finished. | The reminder went to the person assigned to the current step when the check ran. If the step is done, the next check finds nothing. |
| I was not told about a new assignment. | Assignment notifications are sent when the previous step is completed and your step becomes current. They go to whoever holds the step at that moment: the person assigned to it, or else its default assignee, set in the [workflow](/docs/guides/workflows-and-tasks). |
