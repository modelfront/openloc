---
name: tour-ai-prompts
title: AI prompts tour
description: "Every prompt is open: read and change every instruction, add rules per language, choose the context each segment sends, and see the output schema."
roles: [owner, admin, project_manager]
tour:
  - anchor: ai_prompts.list
    page: ai_prompts
    title: Every prompt is open
    body: "Every language-model engine follows a prompt from this library. Every one is open to read; duplicate one to change it."
    say: "Welcome to A I prompts. Every language-model engine follows a prompt from this library, and every prompt is open to read. Duplicate one to make it your own, and change what the model is told, word by word."
  - anchor: ai_prompt.instructions
    pick: ai_prompt
    title: The full instructions
    body: "The complete text the model follows, in plain words. Nothing is added behind your back."
    say: "This is Nordwind's own review prompt. The box shows the complete instructions the model follows, in plain words. Nothing is added in secret, so when a result surprises you, you can read why, and change one sentence to fix it."
  - anchor: ai_prompt.languages
    pick: ai_prompt
    title: Rules per language
    body: "Add a rule for one target language: here German uses the informal du, and French gets vous and French typography."
    say: "Rules for single languages are easy to add. Here German always uses the informal du, which is Nordwind's brand voice, and French uses vous and French punctuation. Each rule is a short sentence, added only when the target language matches."
  - anchor: ai_prompt.context
    pick: ai_prompt
    title: You choose the context
    body: "Pick what each segment sends: TM matches, glossary terms, neighbouring segments and more. The preview shows exactly what goes to the model."
    say: "You also decide what each segment sends to the model, such as translation memory matches, glossary terms or the sentences around it. The preview shows exactly what is sent. Less context means a cheaper call, and more context means a better informed answer."
  - anchor: ai_prompt.schema
    pick: ai_prompt
    title: The output schema
    body: "The reply format and the JSON schema the provider must follow. You see the contract, so scores and fixes never come from a guess."
    say: "Here is the output format, with the JSON schema that every reply must follow. So you see the full contract between the app and the model: what goes in, and exactly what must come back, a score, a fixed translation and a reason."
  - anchor: ai_engines.list
    page: ai_engines
    title: Any language model, one prompt
    body: "A language-model engine joins a provider model with a prompt. Switch the model without rewriting the prompt; improve the prompt and every engine using it follows."
    say: "A language-model engine joins a provider model with one of these prompts. Switch from one provider or model to another and the prompt stays as it is. And when you improve a prompt, every engine and project that uses it follows at once."
  - anchor: mt_prompt.form
    pick: mt_prompt
    title: Translation prompts too
    body: "AI translation has open prompts as well, with the same language rules, context and schema."
    say: "Finally, A I translation has its own prompts, like this one, open in the same way, with the same rules per language and the same context choices. That is prompts here: every instruction open to read and change. Thanks for following along!"

---
Use this when someone asks how AI review or AI translation decides what to do, how to change its instructions, or what a prompt is.

1. Run the saved tour: `start_tour` with `playbook: "tour-ai-prompts"`.
2. For details of one prompt, read it with the prompt tools (`openloc_list_ai_prompts`, `openloc_get_ai_prompt`) and explain its instructions in plain words.
3. To try a prompt on sample text, offer the prompt preview in the app; changing a prompt used by engines is a guarded change that needs the user's confirmation.
