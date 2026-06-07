The user wants to edit an identity. Target: $ARGUMENTS

Steps:
1. Resolve the target file. The argument may be:
   - a bare name ("ml-engineer") → look in ~/.san/identities/<name>.md
     first, then .san/identities/<name>.md
   - a full absolute path → use directly
   If neither exists, tell the user and stop.

2. Read the file first to see its current content.

3. **Use AskUserQuestion if the user's message does not say what to
   change**, or if there are multiple plausible interpretations of the
   change (e.g. "make it more strict" — strict about what?). Skip the
   question if the change is concrete ("add JAX support" → just do it).

4. Use Edit (not Write) to make minimal targeted changes — preserve
   unrelated content and existing section structure.

5. After editing, summarize the diff in 1 line.
