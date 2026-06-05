The user wants to create a new identity (persona) for San.

Optional name hint from the user: $ARGUMENTS

Steps:
1. Read ~/.san/identities/README.md for the format spec. If the file does
   not exist, the format is: frontmatter with `name` and `description`;
   body has sections for Tone / Output / Behavior / Scope / Code conventions.

2. **Use AskUserQuestion if any of these are unclear** from the user's
   message and the optional name hint:
   - persona / role (e.g. "ML engineer", "Go reviewer", "frontend specialist")
   - tone (concise vs. explanatory, formal vs. informal)
   - code style preferences (idiomatic vs. functional, comment density)
   Ask 1-3 questions max — only on dimensions that are genuinely ambiguous.
   Do not ask if the user already specified or strong defaults apply.

3. Pick a kebab-case filename matching the persona ("rust-systems",
   "ml-engineer"). Confirm with AskUserQuestion if uncertain.

4. Write to ~/.san/identities/<name>.md with frontmatter (`name` matching
   the filename + one-line `description`) and body sections (Tone, Output,
   Behavior, Scope, Code conventions).

   Identity-level content only — do NOT include policy / security / git
   rules in the body. Those live in separate system prompt sections that
   always apply regardless of identity.

5. After writing, tell the user to activate via /identity.
