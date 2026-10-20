---
title: "Labels and vendors"
summary: "Classify projects with labels and invite external linguists as vendors."
order: 90
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-17
sources:
  - web/src/pages/LabelsPage.tsx
  - web/src/components/label/**
  - web/src/pages/org/**
  - web/src/components/vendors/**
  - web/src/pages/AcceptInvite.tsx
  - web/src/components/customer/**
  - web/src/components/org/**
  - web/src/components/userpair/**
  - internal/label/**
  - internal/invite/**
related:
  - reference/roles-and-permissions
  - guides/projects-and-files
  - guides/workflows-and-tasks
  - guides/analytics
  - guides/translation-memory
---
A **label** classifies a project: **Customer** (the company you translate for), **Domain** (the subject matter), and any others your organization adds. A **vendor** is an external translator you bring into your organization. Language pairs connect people to work.

## Who can do what

| Action | Owner | Admin | Project manager |
| --- | --- | --- | --- |
| Create labels, add values, and choose a project's labels | ✓ | ✓ | ✓ |
| Delete a value, or a whole label | ✓ | ✓ | — |
| Invite vendors, and resend or revoke their invitations | ✓ | ✓ | ✓ |
| Edit a member's language pairs | ✓ | ✓ | — |

Linguists, including vendors, see none of these pages. See [Roles and permissions](/docs/reference/roles-and-permissions).

## Create a label

1. Open **Labels** in the left menu.
2. Click **Add label**, type a name (up to 200 characters) and click **Add**.

**Customer** and **Domain** are there from the start. They work like any label you add, except that they cannot be deleted — they sit at the top of the list with no bin.

Owners and admins can also reach the same list from **Manage labels** on the **New project** form, without leaving the form.

## Add the values a label allows

A label is a fixed set of choices, and a project picks one of them.

On the **Labels** page, click the label's values field, type the value and choose **Add "…"**. The panel stays open, so several values can be added in a run.

A name — the label's or a value's — cannot be changed once it exists. Projects already carry it, so renaming would quietly rewrite what they were classified as. Add the spelling you want, move the projects to it, then delete the old one.

## Delete a value or a label (owner or admin)

Untick a value to delete it. A value no project uses goes at once. A value marked **In use** asks first: **Delete value?** names how many projects carry it, and **Delete** removes it from those projects and from any template that presets it.

The bin deletes a whole label. It stays greyed out while any of the label's values is in use; hovering it names those values and how many projects use each. Delete those values or change the projects first.

## Put a label on a project

- **New project:** **Customer** and **Domain** are always on the form. **Add label** puts another one there.
- **Existing project:** the same fields sit at the top of the project page. The change is recorded in the project's **History** tab.
- **Template:** a template presets labels, and a project started from it begins with them.

Every label is optional. Translation memories carry a customer of their own, which makes the right memory easy to find when setting up a project.

## Filter projects by customer

On the **Projects** page, click the **Customer** filter chip and pick a value. The **Projects** list shows each project's customer in its row. Filters are part of the page address, so a filtered view can be bookmarked.

## Invite a vendor

Vendors are invited from the **Vendors** tab of the **Organization** page. The account that is created always has the **Linguist** role, so a vendor sees only their own tasks.

1. Open **Organization settings** from the account menu. Owners and admins then click the **Vendors** tab; a project manager lands on it directly.
2. Type the address in **Invite a vendor by email** and click **Send invitation**.

The invitation is emailed with a link that stays valid for seven days. The vendor appears in the table with the status **Not joined yet** and the date invited. An address can have only one pending invitation at a time, and someone who is already a member cannot be invited again.

## Resend an invitation

The invitation link is sent only by email. If the email does not arrive, click the send icon (**Resend invite**) on the pending row of the **Vendors** tab. A new link is emailed, valid for seven days, and the link in any earlier email stops working.

If you send a lot of invitation emails in a short time, new invitations and resends together, the app asks you to wait a minute before you send more.

## Revoke an invitation

On the **Vendors** tab, click the trash icon (**Revoke invite**) on the pending row. The link stops working at once. You can invite the same address again later.

## What the vendor sees

The link opens a **Join organization** page. The vendor either signs in with Google or Microsoft, or, if they have no account yet, enters a **Name** and sets a **Password** (at least 8 characters). Someone who already has an account signs in with the invited address.

After joining, the row on the **Vendors** tab turns **Active** with the date joined. The vendor's left menu has only **Tasks**; **Docs** is in the account menu. They open jobs assigned to them in the CAT editor, translate, run QA and complete their step. They cannot open project pages, browse memories or see other people's jobs. The **Vendors** tab lists every linguist in the organization, not only those invited from it.

## Set a member's language pairs (owner or admin)

Language pairs record which source-to-target combinations a person works in. When you assign a workflow step, the list of people is limited to members who have that pair; owners, admins and project managers are always listed.

1. Open **Organization settings** from the account menu.
2. On the **Members** tab, click **Language pairs** next to the person (or **Edit** if pairs exist).
3. In each row, pick one or more **Sources** and one or more **Targets**. Every source is paired with every target in that row. Click **Add languages** for another row.
4. Check the pair count under the rows and click **Save**.

Pairs appear as small chips next to the member's name. A member may have up to 100 pairs. Saving with no pairs asks you to confirm **Remove all**.
