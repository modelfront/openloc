---
title: "Report an issue"
summary: "Send a bug, an enhancement or a feature request to the support team, and follow the answer."
order: 120
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/components/issues/**
  - web/src/pages/issues/IssuesPage.tsx
  - web/src/pages/issues/IssueDetailPage.tsx
  - web/src/lib/issue-context.ts
  - web/src/lib/issue-labels.ts
  - internal/issues/**
  - internal/mcp/register_issues.go
  - internal/mcp/backend_direct_issues.go
  - web/src/components/chat/ConfirmAction.tsx
  - web/src/components/chat/ChatScreenshotRequest.tsx
  - web/src/components/chat/ChatRepeatRequest.tsx
  - web/src/lib/action-trail.ts
  - web/src/lib/watch-recording.ts
  - web/src/lib/screenshot.ts
related:
  - guides/notifications
  - guides/ai-assistant
---
Anyone in your organization can report a bug or ask for an improvement. The report goes to the support team, and you follow the answer in the app.

## Send a report

1. Click your name in the top right and choose **Report an issue**.
2. Pick the kind of report:
   - **Bug**: something does not work as it should.
   - **Enhancement**: something works, but could work better.
   - **Feature request**: something OpenLoc does not do yet.
3. For a bug, pick the **Severity**:
   - **Minor**: a small problem; your work goes on.
   - **Major**: a real problem, but there is a way around it. This is the default.
   - **Critical**: an important part does not work, and the way around it is hard.
   - **Blocker**: you cannot continue your work.
4. Pick the **Area** of OpenLoc it is about, such as **Editor**, **Translation memory**, **Workflow**, **Projects**, **Tasks**, **AI assistant (chat)** or **MCP server**. Leave **Other** when none fits.
5. Give it a **Title** and describe **What happened?** For a bug, say what you did, what you expected and what happened instead.
6. To add files, click **Attach files**, or paste or drop an image into the description. An image goes into the text where your cursor is and shows there; other files stay in the list below. Any type of file is accepted: screenshots, logs, the document you were working on. You can attach up to 10 files of 25 MB each.
7. Click **Send report**.

The support team may change the kind, the severity or the area after reading your report. When a report becomes a bug, its severity starts at **Major**; when a bug becomes an enhancement or a feature request, it loses its severity.

## Change the kind

You can fix the kind yourself: open the report and pick another kind under **Kind**; it saves at once. The reporter and a manager who sees the report can do this. As when the support team changes it, a report that becomes a bug starts at **Major**, and a bug that becomes something else loses its severity. The conversation records "changed the kind to …"; nobody is notified.

## Report it with the assistant

When your organization has the assistant set up, the **Report an issue** form offers **Ask the assistant**. It opens the assistant; describe the problem in your own words. The assistant asks for what it still needs, such as the severity of a bug, and shows the report in a card. This works in read-only mode too.

To see the problem, the assistant may ask for a screenshot or for you to repeat the steps. Nothing is captured until you click, and you can **Skip** either.

- **Add screenshot** takes a picture of the page you are on, without the assistant panel. The chat shows it before you decide: **Send** gives it to the assistant, **Skip** drops it. If the assistant does not answer, the button reads **Send again**. An assistant whose model cannot view images says so, and the screenshot still goes with the report.
- **Start recording** hides the assistant panel; the conversation stays. A banner stays on top of the page, "Do it again — I’ll record it for the team. 🎬", with the time left and **Done**. Repeat the steps, then click **Done**. The recording also stops by itself after 3 minutes. Either way the panel comes back and the assistant carries on.

A recording holds the same things every time: what you do while it runs, what you type (never in a password, card or one-time-code field), the start of the answer to each failed request, and one screenshot at the first error. The steps go with the report to the support team only, and the screenshot is a file on the report like any other. The assistant only hears that a recording is attached.

Click **Review and send** on the card. The **Report an issue** form opens, filled in by the assistant. Up to 3 of the screenshots you sent the assistant, and the one from the recording, are among the files, where you can remove them; a screenshot that has expired is left out. To leave out the steps you recorded, click the **×** next to them. Check the form, change what you like, attach files or paste a screenshot, and click **Send report**. The report goes with the same details as any other, plus **Sent through: AI assistant**. Nothing is sent until you click **Send report**.

You can also just ask the assistant, or an AI tool connected to OpenLoc, to report an issue at any time.

If a page fails to load and shows an error panel, click **Report this issue** on that panel. The title is filled in from the error message.

You can send up to 10 reports and 30 replies an hour.

## What goes with your report

Under **We’ll include**, the form shows what is sent along with your text:

| Item | Example |
|---|---|
| **Page** | `/projects/12/jobs/34/edit` |
| **Project**, **File**, **Job** | The ids in the page's address, when there are any. |
| **Browser** and **Operating system** | Chrome 131, macOS |
| **Window size** | 1440x900 |
| **App version** | The version of OpenLoc you are using. |
| **Role** | Your role in this organization. |
| **Your recent actions** | The last 50 page changes, clicks, failed requests, error messages and notices, never anything you typed. Click it to read the list. |
| **The steps you recorded** | Only on a report the assistant wrote after you recorded the steps: what happened in order, including what you typed and the start of each failed answer. |

The parts of an address after a question mark are never sent, and neither are the codes in share, invitation and password-reset links.

Your recent actions and the steps you recorded reach the support team only, and no AI reads them. A bug the assistant writes may also carry its own check, **Verified**, **Not reproduced** or **Unverified**, with one line of what it saw. Only the support team sees it; the form does not show it.

## Follow your reports

Click your name in the top right and choose **Reported issues**.

- Owners, admins and project managers see every report of the organization, with who sent it.
- Linguists see the reports they sent.

The list shows the reports that still need work, the most recently updated first. Click the **Updated** heading to show the oldest first. Search by a word in the title or the description, and filter by status, kind, severity and area. The search works within the filters: the default shows only reports that still need work, so pick **Resolved**, **Won't fix** or **Duplicate** under **Status** to find a closed one. **Clear filters** resets the search too. An icon before each title shows its kind; point at it to see the name. A bug's severity is at the end of the title.

Under each status, a chip shows who has to act next, **Support team** or **Your organization**. It is yellow when the turn is your organization's, and closed reports show none. The report page reads it as **Waiting on**.

| Status | Meaning |
|---|---|
| **Open** | Waiting for the support team, or for you when the team has replied and left the report open. |
| **In progress** | The team is working on it. |
| **Needs info** | The team needs more details from you. |
| **Resolved** | Fixed or answered. |
| **Won't fix** | The team decided not to change this. |
| **Duplicate** | The team tracks the problem in another report. The page always shows the original's number and status, and its title and a link only when you may open it. If that report is deleted, yours goes back to **Open**, without a notification. |

Click a report to read the conversation. Each reply, status change, assignment and fix confirmation appears in order, oldest first. Images pasted into a reply show in the text; click any other file to download it. Once someone on the support team takes the report, the page reads **Assigned to** and their name.

## Answer the team or add details

Open the report, write in the box under the conversation, attach files if they help (an image pasted or dropped goes in the text where your cursor is), and click **Send**. Anyone who sees a report can reply to it, so a manager can answer for a colleague.

A reply puts a **Needs info** report back to **Open**. A reply to a **Resolved**, **Won't fix** or **Duplicate** report reopens it.

## Confirm a fix

A **Resolved** report asks **Is it fixed for you?**

- **Confirmed fixed** records that you checked the fix. The question goes away and the report shows a **Fix confirmed** chip.
- **Still broken** takes you to the reply box. Say what is still wrong and send it: the reply reopens the report.

Anyone who sees the report can answer.

## Delete a report

Point at a report in the list and click the trash icon at the end of its row, or open it and click the trash icon at the top right, then confirm. The report leaves the list, for you and for the support team.

- Everyone can delete the reports they sent.
- Owners can delete every report of the organization.
- Admins can also delete the reports of project managers and linguists, and project managers those of linguists.
- A report whose sender has left the organization can be deleted by any owner, admin or project manager.

The trash icon shows only on the reports you can delete.

## Get told about answers

When the support team replies or changes the status, you get a notification under the bell and an email. The emails about one report share the subject "Issue #<id>: <title>" and stay in one thread. Click the notification to open the report. To stop the emails, untick **Issue updates** in your personal Settings; see [Notifications](/docs/guides/notifications#mute-emails-you-do-not-want).

Owners, admins and project managers also get a bell notification when the support team resolves any report of the organization; see [Notifications](/docs/guides/notifications#know-which-events-create-a-notification).

**Report an issue** is not offered inside the Playground, and some OpenLoc installations do not take reports. When the menu has no **Report an issue** item, ask whoever runs your OpenLoc.
