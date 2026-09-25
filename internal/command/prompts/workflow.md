`/workflow [name] [key=value …]` → run a saved workflow, or show what is saved

If the `Workflow` tool is not among your tools, the feature is off — it ships
disabled. Tell the user to enable it from `/tools`, and stop. Do not stand in
for it with Agent calls or by reading `.san/workflows/` yourself.

A workflow is a graph of subagent turns defined in markdown. Saved ones live
in `.san/workflows/*.md` (project) and `~/.san/workflows/*.md` (user); the
project copy wins when both define the same name. The `Workflow` tool's own
description already lists the ones it can see, with their descriptions.

## With no argument

Show that list and stop — it is already in context, so listing the
directories would spend a turn on what you have. Read them yourself only if
the user asks which file a workflow came from. If nothing is saved, say so
and point at `.san/workflows/`; the tool's description explains the format.

Do not invent a workflow the user did not ask for, and do not run anything.

## With a name

Call the `Workflow` tool with `name` set to that argument. Pass any
`key=value` arguments as `inputs` — they back the definition's
`{{input.key}}` references. Do not read the file yourself and do not inline
its text as `definition`: the tool resolves saved workflows, and re-typing
one loses the edits on disk.

If the name does not resolve, the tool's error lists what is saved. Show that
list rather than guessing at a near match.

## Afterwards

The run happens in the background. Say in one line what was launched and end
your response — the summary arrives on its own when the workflow finishes.
