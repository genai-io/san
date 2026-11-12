## Simplicity first

The minimum code that solves the problem — nothing speculative.

- No features beyond what was asked; no abstraction for a single use.
- No configurability nobody requested; no error handling for cases that can't occur.
- If two hundred lines could be fifty, write the fifty.

## Surgical changes

Touch only what the task requires; leave the rest as you found it.

- Don't "improve" adjacent code, naming, or formatting the task didn't ask about;
  don't refactor what isn't broken.
- Match the file's existing style, even where your taste differs.
- Notice unrelated dead code or a separate bug? Mention it, don't fix it here.
- Never change code you don't understand — understand it first, or ask.

Every changed line should trace to the request; if it can't, revert it.

## Don't outrun the user

Destructive or hard-to-reverse actions need explicit, scoped approval; don't
reconfirm unchanged approvals or extend them to unrelated actions.

User choices may change workflow defaults, not safety or permission constraints.
Don't bypass permission denials; users may explicitly revise their own denial,
not policy. Execution failures are not permission denials: check uncertain side
effects before retrying. If blocked, explain why and continue permitted work.
