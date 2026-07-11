# Contributor Ladder

Welcome! This contributor ladder outlines the different contributor roles
in San, along with the responsibilities and privileges that come with
them. Community members generally start at the lower rungs and advance as
their involvement in the project grows. Existing project members are happy
to help you move up the ladder.

Each role below is described with three kinds of things:

- **Responsibilities** — things a contributor in that role is expected to do.
- **Requirements** — qualifications needed to reach that role.
- **Privileges** — things a contributor at that level is entitled to.

The roles map directly onto the [`OWNERS`](OWNERS) file, which is the
single source of truth for who reviews and approves changes. If you are
just getting started, read [`CONTRIBUTING.md`](CONTRIBUTING.md) first.

- [Contributor Ladder](#contributor-ladder)
  - [Community Contributor](#community-contributor)
  - [Reviewer](#reviewer)
  - [Approver](#approver)
  - [Maintainer](#maintainer)
- [Inactivity](#inactivity)
- [Involuntary Removal or Demotion](#involuntary-removal-or-demotion)
- [Stepping Down/Emeritus Process](#stepping-downemeritus-process)
- [Contact](#contact)

## Contributor Ladder

| Role | Listed in `OWNERS` | Can review | Can approve merges | Owns direction |
|------|--------------------|:----------:|:------------------:|:--------------:|
| **Community Contributor** | — | — | — | — |
| **Reviewer** | `reviewers` | ✅ | — | — |
| **Approver** | `approvers` | ✅ | ✅ (their area) | partial |
| **Maintainer** | root `approvers` | ✅ | ✅ (whole repo) | ✅ |

Each rung carries the responsibilities and privileges of the rungs below
it. Advancement is based on sustained, high-quality participation — not on
a fixed quota of merged PRs.

### Community Contributor

Description: Anyone who contributes to the project. Contributions need not
be code — filing a good bug report, improving docs, reviewing a PR,
triaging issues, or helping others in discussions all count.

- Responsibilities:
  - Follow the [Code of Conduct](CODE_OF_CONDUCT.md).
  - Follow [`CONTRIBUTING.md`](CONTRIBUTING.md): sign off commits with DCO
    (`git commit -s`), use
    [Conventional Commits](https://www.conventionalcommits.org/), and run
    `make ci` before opening a PR.
- Ways to get involved:
  - Report bugs and comment on issues.
  - Submit pull requests.
  - Improve documentation.
  - Answer questions in
    [Discussions](https://github.com/genai-io/san/discussions).
  - Test releases and review other contributors' PRs.
- Privileges:
  - Eligible to become a Reviewer.

There is no application step — every good-faith participant is a
contributor.

### Reviewer

Description: A Reviewer is responsible for a specific area of the project —
a package or set of directories, a section of the docs, or another
clearly-defined component. They are collectively responsible, with other
Reviewers of that area, for reviewing changes to it and signalling whether
they are ready to merge. Reviewers are listed under `reviewers` in the
[`OWNERS`](OWNERS) file for their area.

Reviewers have all the responsibilities and privileges of a Community
Contributor, plus:

- Responsibilities:
  - Review most pull requests against their area, focusing on correctness,
    design, and risk.
  - Help triage incoming issues and reproduce bugs.
  - Help new contributors get their PRs into a mergeable shape.
- Requirements:
  - A track record of multiple non-trivial merged PRs in the area.
  - Demonstrated knowledge of the relevant packages and of the
    [dependency rules](docs/reference/dependency-rules.md) and
    [design principles](docs/design/principles.md).
  - Timely, constructive reviews.
- Privileges:
  - Their review is a strong signal toward merging a change in their area.
  - May recommend other contributors to become Reviewers.

Process of becoming a Reviewer:

1. An existing Approver or Maintainer nominates the contributor by opening
   a PR adding their GitHub username to the `reviewers` list of the
   relevant [`OWNERS`](OWNERS) file, linking to representative work.
2. At least one other Approver approves the PR; it merges under lazy
   consensus (no Approver or Maintainer objects within a reasonable
   window).

### Approver

Description: An Approver owns the quality of an area of the codebase and
can give the final approval that merges a change there. Approvers are
listed under `approvers` in the [`OWNERS`](OWNERS) file.

Approvers have all the responsibilities and privileges of a Reviewer, plus:

- Responsibilities:
  - Give the final review before a change merges, ensuring it meets the
    project's correctness, design, testing, and documentation bar.
  - Keep their area healthy — tests, docs, and dependency rules stay
    intact (see [`docs/packages/`](docs/packages/)).
  - Mentor and nominate new Reviewers.
- Requirements:
  - Everything expected of a Reviewer, sustained over roughly three months
    or more of active participation.
  - Deep familiarity with the area — its packages, tests, and pitfalls.
  - Sound judgment about what belongs in the project.
- Privileges:
  - Their approval is sufficient to merge a change in their area.
  - May nominate new Reviewers and, with Maintainer sign-off, new
    Approvers.

Process of becoming an Approver:

1. An existing Approver or Maintainer opens a PR adding the candidate to
   the `approvers` list of the relevant [`OWNERS`](OWNERS) file, with a
   short rationale and links to their work.
2. The candidate comments on the PR agreeing to the responsibilities of
   the role.
3. At least two existing Approvers approve (or all Maintainers, if fewer
   than two apply); it merges under lazy consensus.

### Maintainer

Description: Maintainers are established contributors responsible for the
project as a whole. They can approve changes to any area and set the
project's strategy and priorities. Maintainers are the approvers listed in
the **root** [`OWNERS`](OWNERS) file.

Maintainers have all the responsibilities and privileges of an Approver,
plus:

- Responsibilities:
  - Steward overall direction, architecture, and the roadmap.
  - Cut releases (see
    [`docs/operations/release.md`](docs/operations/release.md)) and keep
    CI, tooling, and the contributor experience healthy.
  - Handle security reports per [`SECURITY.md`](SECURITY.md) and enforce
    the [Code of Conduct](CODE_OF_CONDUCT.md).
  - Mentor new Reviewers and Approvers.
  - Make the final call when lazy consensus does not resolve a
    disagreement.
- Requirements:
  - A sustained record as an Approver across multiple areas.
  - Broad knowledge of the project and judgment exercised for its good,
    independent of employer or team.
  - Willingness to take on project-wide responsibility.
- Privileges:
  - Approve and merge changes anywhere in the repository.
  - Administrative access to the repository and the release process.
  - Represent the project in public as a Maintainer.

Process of becoming a Maintainer:

1. Any current Maintainer nominates a current Approver by opening a PR
   adding them to the `approvers` list of the root [`OWNERS`](OWNERS)
   file.
2. The nominee comments on the PR agreeing to the responsibilities of the
   role.
3. A majority of the current Maintainers approve the PR.

## Inactivity

It is important for contributors to stay active, both to move the project
forward and to keep the `OWNERS` lists an accurate picture of who is
available. Inactivity is measured by:

- No contributions for longer than six months.
- No response to reviews or mentions for longer than six months.

A Reviewer, Approver, or Maintainer who becomes inactive may be asked to
move to emeritus status and be removed from the active `OWNERS` lists,
after a heads-up. This is not a judgment on past work.

## Involuntary Removal or Demotion

Involuntary removal or demotion happens when a contributor is not meeting
the responsibilities or requirements of their role — for example, a
sustained period of inactivity, a persistent failure to meet the role's
requirements, or a violation of the [Code of Conduct](CODE_OF_CONDUCT.md).
This protects the community and its deliverables and makes room for new
contributors to step in.

Involuntary removal or demotion is decided by a majority vote of the
current Maintainers.

## Stepping Down/Emeritus Process

Commitment levels change, and that is fine. A contributor may step down a
rung or move to emeritus status (stepping away from active duties) at any
time by opening a PR that updates or removes their entry in the relevant
[`OWNERS`](OWNERS) file, or by contacting the Maintainers. Emeritus
contributors are recognized for their past work and are welcome to return
through the same nomination process that first added them.

## Contact

For questions about the contributor ladder, mentorship, or moving up a
rung, start a thread in
[Discussions](https://github.com/genai-io/san/discussions) or reach out to
the Maintainers listed in the [`OWNERS`](OWNERS) file. For conduct
concerns see the [Code of Conduct](CODE_OF_CONDUCT.md); for security issues
see [`SECURITY.md`](SECURITY.md).

---

This contributor ladder is adapted from the
[CNCF Contributor Ladder template](https://github.com/cncf/project-template/blob/main/CONTRIBUTOR_LADDER.md).
