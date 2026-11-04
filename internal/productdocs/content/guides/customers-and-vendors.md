---
title: "Labels and vendors"
summary: "Classify projects with labels, invite vendor organizations, and manage members."
order: 90
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-03
sources:
  - web/src/pages/LabelsPage.tsx
  - web/src/components/label/**
  - web/src/pages/org/**
  - web/src/pages/AcceptInvite.tsx
  - web/src/components/customer/**
  - web/src/components/project/ProjectFilterBar.tsx
  - web/src/components/org/**
  - web/src/components/userpair/**
  - internal/label/**
  - internal/invite/**
  - internal/api/vendor_admin_notice.go
  - internal/vendoridle/**
  - internal/auth/member_enable.go
  - web/src/pages/OrgPicker.tsx
  - web/src/components/OrgSwitcher.tsx
  - web/src/pages/AwaitingApproval.tsx
limitations:
  - vendor-org-no-work
related:
  - reference/roles-and-permissions
  - guides/projects-and-files
  - guides/workflows-and-tasks
  - guides/analytics
  - guides/translation-memory
---
A **label** classifies a project: **Customer** (the company you translate for), **Domain** (the subject matter), and any others your organization adds. A **vendor** is a translation company with an organization of its own, linked to yours. Language pairs connect people to work.

## Who can do what

| Action | Owner | Admin | Project manager |
| --- | --- | --- | --- |
| Create labels, add values, and choose a project's labels | ✓ | ✓ | ✓ |
| Delete a value, or a whole label | ✓ | ✓ | — |
| Invite a vendor, and resend or revoke that invitation | ✓ | ✓ | — |
| Invite your own admins, project managers and linguists, and resend or revoke those invitations | ✓ | ✓ | Linguists only, through the API or a connected AI tool |
| Edit a member's role and language pairs | ✓ | ✓ | — |

Linguists see none of these pages. See [Roles and permissions](/docs/reference/roles-and-permissions).

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

On the **Projects** page, click the filter icon at the end of the filter bar, pick **Customer**, then pick a value; the customer then shows as a chip on the bar. The **Projects** list shows each project's customer in its row. Filters are part of the page address, so a filtered view can be bookmarked.

## Invite a vendor (owner or admin)

A vendor is its own organization. Its people, roles and settings are separate from yours.

1. Open **Organization settings** from the account menu.
2. Click **Invite vendor** at the top of the **Vendors** list. In the window, type the company's name in **Vendor name** (up to 120 characters) and the address of the person who will run it in **Admin email**. Click **Send invite**.

The invitation is emailed with a link that stays valid for seven days. It asks the person to set up the vendor with you as its client, and to be its admin. Accepting creates the vendor organization; it does not make the person a member of yours.

On your **Members** tab the invitation appears under **Pending invites** with a **Vendor org admin** label, the vendor name and the expiry date. Once accepted, it moves to **Accepted invites**, with the same label, the vendor name and the date it was accepted, and the vendor organization is listed under **Vendors**, below **Members**, with its admins, the languages its people work in and the date it joined. Search the list by vendor name or admin email, or filter it by **Source** and **Target** language to find a vendor for a language pair; it shows 10 vendors per page. An address can have only one pending invitation at a time, and someone who is already a member cannot be invited again.

## Invite a member

**Invite member**, at the top of the members list, opens a window with **Email** and a **Role**: **Admin**, **Project manager** or **Linguist**. A linguist you invite here is an in-house linguist: they join your organization and see only their own tasks. Their invitation appears under **Pending invites** with that role.

## Resend an invitation

The invitation link is sent only by email. If the email does not arrive, click the send icon (**Resend invite**) on the invitation's row under **Pending invites**. A new link is emailed, valid for seven days, and the link in any earlier email stops working.

If you send a lot of invitation emails in a short time, new invitations and resends together, the app asks you to wait a minute before you send more.

## Revoke an invitation

Under **Pending invites**, click the trash icon (**Revoke invite**) on the invitation's row. The link stops working at once. You can invite the same address again later.

## What the invitee sees

The link opens a **Join organization** page. The invitee either signs in with Google or Microsoft, or, if they have no account yet, enters a **Name** and sets a **Password** (at least 8 characters). Someone who already has an account signs in with the invited address.

An in-house linguist is then listed under **Members** with the **Linguist** role. Their left menu has only **Tasks**; **Docs** is in the account menu. They open jobs assigned to them in the CAT editor, translate, run QA and complete their step. They cannot open project pages, browse memories or see other people's jobs.

## Run a vendor organization

A **Vendor** chip next to the organization's name, in the header and in the organization picker, marks a vendor organization. The vendor's admin opens **Organization settings** in the vendor organization. The page is titled **Organization** with **Vendor for** and your organization's name under it, and it has only the **Members** tab. For its roles and what each one sees, see [Vendor organizations](/docs/reference/roles-and-permissions#vendor-organizations).

- **Invite member** offers **Project manager** and **Linguist**. To make someone an admin, change their role in the member window. When someone becomes an admin, the client's owners and admins get a notification in their bell; clicking it opens their **Vendors** list.
- The vendor organization works only while your organization is active.

### Access after 90 days without use

Someone who has not used the vendor organization for 90 days loses access to it. Seven days before, they get an email asking them to sign in; using the organization at any time starts the 90 days again. Only their access to that vendor organization ends: their account and any other organizations stay open.

They then see **Your access to** and the vendor's name **is disabled** when they open it, and the organization picker marks it **Disabled**. In the vendor's **Members** list they have a **Disabled** chip. Open their row: the window says **Access disabled** and has **Enable**.

- The vendor's admins enable any member: an admin, a project manager or a linguist. Project managers and linguists cannot enable anyone.
- In your **Vendors** list, a vendor with disabled admins has a **Disabled** chip next to its name, and only its enabled admins are listed under it. Open the vendor's row to see all its admins, and **Enable** the disabled ones you choose. Your owners and admins can enable a vendor's admins at any time, for example when all of them are disabled.

## Set a member's role and language pairs (owner or admin)

Language pairs record which source-to-target combinations a person works in. When you assign a workflow step, the list of people is limited to members who have that pair; owners, admins and project managers are always listed. Each organization sets its own pairs for a person: someone who is also in a vendor organization, or in another organization, can have different pairs there, and they never change yours.

1. Open **Organization settings** from the account menu.
2. On the **Members** tab, click the person's row. The window has their **Role** and their **Language pairs**. The list shows owners first, then admins, project managers and linguists, 10 per page, with page numbers under the list. To find someone, search by name or email, or narrow it with the **Role**, **Source** and **Target** filters. Each filter lists only the values that still find someone, given the other filters.
3. To change the role, pick another one in **Role**. Switching someone down to **Linguist** asks you to confirm, because they lose project-manager access.
4. In each language-pair row, pick one or more **Sources** and one or more **Targets**. Every source is paired with every target in that row. Click **Add languages** for another row.
5. Check the pair count under the rows and click **Save**. **Save** stays unavailable until something changes, and one click stores the role and the pairs together.

Pairs appear as small chips next to the member's name. A member may have up to 100 pairs. Saving with no pairs asks you to confirm **Remove all**.
