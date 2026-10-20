---
name: tour-assistant-and-mcp
title: Assistant and MCP tour
description: "An assistant that can do almost anything in the app: create and run projects, answer from the docs and your memories, build dashboards and tours — and the same from Claude or ChatGPT over MCP."
roles: [owner, admin, project_manager, linguist]
tour:
  - anchor: chat.launcher
    title: The assistant
    body: "An AI assistant on every page, bottom right. It works with your projects, jobs, memories and settings."
    say: "Welcome! Down here in the bottom right corner is the A I assistant, on every page. It is not only a help bot. It works with your real projects, jobs, memories and settings, so you can do almost anything in the app just by asking."
  - anchor: chat.example.project
    title: Create and run a project
    body: "Upload a file and ask. It creates the project, adds the file and pre-translates it."
    say: "Here is an example. A manager uploads a file and asks for a project into German and French. The assistant creates the project with the right template, adds the file and starts the pre-translation, all from one sentence."
  - anchor: chat.example.knowledge
    title: Answers from your data
    body: "It searches the glossary, the translation memories and the docs, and answers with sources."
    say: "It also finds useful information for you. Here it answers a question from the glossary, the translation memories and the docs together, so you get one clear answer instead of searching three pages yourself."
  - anchor: chat.example.tour
    title: Tours on request
    body: "Ask how to do something and it can show you, as a guided tour on the real page."
    say: "When you ask how to do something, it can show you on the real page, as a guided tour like this one. It makes the tour on request, step by step, for the exact task you asked about."
  - anchor: chat.example.confirm
    title: You stay in control
    body: "Read-only by default. In write mode, every delete or shared change waits for your click."
    say: "You stay in control. The assistant starts in read-only mode. In write mode it can change things, but anything that deletes or changes a shared setting shows a card like this one, and nothing happens until you click to confirm."
  - anchor: ai_settings.connect
    page: ai_settings
    title: From your own AI tools
    body: "Paste this address into Claude or ChatGPT and sign in. The tool works as you, in this workspace."
    say: "The same abilities work outside the app, through the Model Context Protocol, or M C P. Here in A I settings you copy one address, paste it into Claude or ChatGPT, and approve. No key to copy or keep secret."
  - anchor: ai_settings.tokens
    page: ai_settings
    title: Access keys
    body: "For a tool that cannot sign in, or your own scripts: create a personal access key."
    say: "Some tools cannot sign in, and your own scripts never do. For those, create a personal access key here. Give it a name and an expiry date, choose read only or read and write, and keep it secret, because anyone who has the key acts as you."
  - anchor: settings.connected_apps
    page: settings
    title: Connected apps
    body: "Claude, ChatGPT and other apps you connected by signing in. Disconnect one here at any time."
    say: "Every app you connected by signing in is listed here, and you can disconnect one at any time. That is the assistant: your whole workspace, one question away, in the app or in the tools you already use."

---
Use this when someone asks what the assistant can do, whether it can change things, or how to use OpenLoc from Claude, ChatGPT or another AI tool (MCP).

1. Run the saved tour: `start_tour` with `playbook: "tour-assistant-and-mcp"`.
2. For setup questions, open the MCP docs page (`navigate_to` page `docs`, slug `guides/mcp`) and answer from it (`openloc_get_doc_page`).
3. Never ask for or repeat a token or key in the conversation; the user creates tokens in AI settings themselves.
