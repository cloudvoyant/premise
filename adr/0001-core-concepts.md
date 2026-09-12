# Core concepts and terminology

- Status: proposed
- Deciders: Siddharth K
- Date: 2026-09-09

## Context and Problem Statement

All projects go through the same lifecycle and have the same needs and growing pains:

- All projects start somewhere, from a template or from scratch, or nowadays via llm generation
- All projects have similar tasks which need to be performed - building, testing, linting, formatting and deployment. In the absence of shared task runners like mise, task.dev, justfile, makefile, npm taks, uv+poe, shared scripts, people have to reinvent the wheel or end up doing things in inconsistent ways. In the worst case, things break.
- The above tasks need to be performed in CI, and often the way things are done in CI does not match up to how things are done locally. Furthermore, CI execution environment often does not match local development, nor do developers' environments across a team match each others.
- At some point, libraries and tools need to be shared via package managers, releases, containerization or vcs based distribution.
- For applications deployed on the web, or involving a web based component like auth or storage sync even, there has to be infra which needs to be managed. Contemporary teams use IaC tools for this job like Terraform, Pulumi, CloudFormation, SST, Helm, Crossplane, etc., or in some cases script resource creation.
- Even with these tools in play, still decisions need to be made about environments, CI integration, VCS flows, secret and credential management, etc.
- With all these hurdles cleared, applicationd developers still have to choose application frameworks, languages, libraries and SDKs, which of course need to play nicely with a lot of the above choices.
- Finally, none of these choices are set in stone. Technologies change, team preferences change, better tools become available, mistakes are made and bitter lessons learned. All of which is to say, the above set of intertwined choices then need to EVOLVE. Somehow, in the face of real deadlines and commercial pressures.

It would be great if this were all. But there are some newer developments on these age old issues in the age of LLMS:

- With LLMs/agents in the mix, there's opportunities for quicker evolution, but also things breaking at any of the fault lines in the systems illustrated above. An agent may fix a "problem" by performing tasks in the way of a wayward team member, violating undocumented invariants, or in the comically extreme cases, hopscotch past thin guarails and take down production resources doing some real damage.
- Starting new projects from scratch can introduce additional complexities with LLMs making a lot of the above choices in inconsistent ways across projects. It is also known that LLMs are as yet weak at "architecture", and rest assured a lot of the above choices firmly reside in that realm.

Given all this, there's a legitimate need for:

- Quickly getting new projects up and runnning, with a lot of the important choices above made and made well.
- Allow projects to evolve safely and swiftly across application code, infrastrcture, CI and dev tooling
- Prevent LLMs from doing harm in ways that compromises the ability of human contributors on software projects
- Enforce guarails on agents and allow them to only operate in team-sanctioned ways

Solving a lot of the above problems falls under the domain of "platform engineering", as its called today. A common solution coming to the fore for some of these platforms is the use of cloud platforms like HashiCorp Cloud, PulumiCloud, Scalr, etc. for managing IaC modules together with internal developer portalsi (Backstage, Portal, Cortext, etc.) for managing "product catalogs" built on these, and enable some form of self service. Agentic integrations are also upcming in these products. However, these are non trivial to set up and learn, and hard to adopt from the get go. Certainly, a lot of these solutions are not open source either. None of these address the concept of evolution at all.

There is perhaps a need for something more lightweight, open source and git native, that can leverage the best of modern tooling which is now available.

## Decision Drivers

Premise aims to be a lightweight platform engineering tool that enables all the above. The goal is to bring the best-in-class tools for IaC, CI, task-management, templating, etc. together in a cohesive package that supports starting new projecs and evolving them, without imposing the massive burden of a new tool to learn for application developers (and ideally platform engineers as well).

The core ideas are:

1. Do not reinvent the wheels - lots of amazing tools exist to solve a lot of the above problems.

- mise and devbox are excellent tools for environment management
- mise, task.dev, justfiles are among some of the best task-runners out there with excellent DX
- most package managers now natively support monorepo workspaces, reducing the need for heavyweight tooling like nx, bazel, lerna, turborepo, etc.
- IaC tools like terraform and pulumi offer excellent declarative IaC management; Pulumi in particular is easy to distribute as well

1. Convention over configuration? -> Ideally a new project sets you up good guardrails that don't interfere with your work, but then lets you go. No need to figure out CI structure/triggers, no need to figure out environments, no need to figure out how to do monorepos, no need to figure out your vcs flow, etc.
1. Tooling as a tailwind - tooling should support good dx by lowering the friction of the hard parts, reducing decision making around arbitrary yet non-trivial decisions. Infra updartes should just be done, you should be writing your infra modules, not figuring out how to script them and wire them into CI, for example.
1. Keep things light, do not create a massive new monorepo tool to learn
1. Leverage LLMs strengths and mitigate their weaknesses

## Considered Options

### Premise as a lightweight convention engine

- Premise owns the following:
  - templates and projects -> premise "projects" can be composed from one or more live "templates" which can be tested by ensuring their CI is live and tests their implementation. This invariant can be used to test the health of instantiated projects.
  - migrations can be created via CLI and implemented via controlled LLM invocation -> this obviates the need for complex scripting and adopting treesitter or openrewrite based modifications
- Premise delegates the following:
  - environment is standardized via tools like mise and devbox (not dev containers for many reasons), and template constrained
  - tasks are owned by task runners like mise (to start), task.dev or justfiles, but premize may provide some dx convenience on top of this if needed
- Premise enforces conventions in the following ways:
  - premise hooks in standard tasks into CI automatically (treating VSC and CI runners as CI backends) -> this means that running things locally is done in the same way as in CI and we can force agents to use these same methods
  - infra declaration and environments are managed via convention, and infra planning and execution can be owned by premise CLI so application developers and platform engineers can simply write IaC modules and let premise shuttle off IaC modules and states into desired backends for those
  - Premise expects IaC provider auth to defined and supported for CI as well as local dev via templates

In this way, a premise template becomes equivalent to a product catalog entry which additionally supports evolution via LLM powered migrations.

### Spectrum of choices

There is a full spectrum between the above and below sections, only extremes are portrayed.

### Premise as a full blown monorepo tool, with native composition support

- Premise offers templates with bases and blocks, supporting templates with conditional composition allowing for flexible initialization (use this db, this orm, this ci backend fw, this ui lib, this styling lib, etc.)
- Premise manages a monorepo natively as well as environments
- Premise templates own infra modules individually, and premise owns tightly controlled providers
- Premise provides native task management
- Premise hooks into CI providers on their own terms

## Decision Outcome

### Premise will manage IDPs through evolvable templates

A template can look something like this:

```text
premise-tanstack/
|-- apps/
|   |-- premise-tanstack/
|       |-- src/
|       |-- test/
|       |-- e2e/
|       |-- package.json
|       |-- mise.toml (build, lint, format, test, run/dev, clean, publish, dotenv)
|-- docs/
|-- infra/
|   |-- components/    (pulumi components)
|   |-- environments/  (pulumi stacks for template's applications/services)
|   |-- providers/
|       |-- kinde/     (mise tasks: auth:login|status|configure)
|       |-- vercel/
|       |-- neon/
|-- libs/
|   |-- ui/
|   |-- db/ (mise.toml w/ seed, migrate:up|down)
|-- migrations/
|   |-- v0.1.txt
|   |-- v0.2.txt
|-- pnpm-workspace.yaml
|-- package.json
|-- premise.yaml
|-- mise.toml (defines any tools, and global tasks install, build, test, lint, format)
```

premise.yaml would define the template variables, and questionaire:

```yaml
templates:
  - name: premise-tanstack
    kind: app|lib
    version: 0.1.0 (or maybe just on git tag?)
    questions:
      - prompt: "App name:"
        type: string
        populate: "appName"
        populateFlag: "a"
      - prompt: "Database provider:"
        type: radio
        choices:
          - "Neon Postgress"
          - "MongoDB Atlas"
    substituions:
      - premise-tanstack: "{appName}"
```

A user can create a new premise project like so:

```bash
pm init
pm g cloudvoyant/premise-tanstack # omitting cloudvoyant/premise- will use
                                  # cloudvoyant as the default org and prepend premise-
                                  # to template repo name. Error if no repo found.
```

The user would be prompted based on the template's questions from its premise.yaml file, and then the dir would be populated based on the template via premise's scaffolding engine. The projects mostly empty premise.yaml will be updated:

```yaml
workspace:
  name: my-project
  schema-version: 0.1 # tied to premise version

  providers:
    ci: github                       # future gitlab
    tools: mise                      # future devbox
    tasks: mise                      # future tasks.dev, justfiles
    infra: pulumi                    # future opentofu
    versioning: svu                  # packaged with GoReleaser automation

  projects:
    - name: my-tanstack-app
      template: cloudvoyant/premise-tanstack
      version: 0.1.0
      path: apps/my-project
```

From this point the user shoud be able to use premise or mise to run tasks:

```bash
mise run //apps/my-app:build
pm run my-app:build
pmx my-app:build
```

Users should be able to generate additional tasks via `pm g :task` or template specific files via `pm g cloudvoyant/premise-tanstack:route|serverFunction`, etc.

Premise init command would also generate github actions dir which would utilize the global action provided by the premise repo to power the fllowing workflows:

- on-commit: branches on every project, runs install -> build -> test -> lint+fmt -> publish rc, then *:db:migrate and infra update for preview env
- on-merge: same as above but upversion -> publish (not rc), and then dev env *:db:migrate and infra update, and preview tear down
- on-release: db migrate and infra update for stage or prod depending on CI arg

If used templates do not have db tasks or infra is absent, then those just get skipped. These workflows will trigger as implied by the workflow names.

Version prediction and tagging will use svu, while GoReleaser will build and publish release artifacts. There will be repo wide grouped versioning, but some templates may additionally provide sub package level versioning. For example golang requires packageName/MAJOR.MINOR.PATCH tags for imports (unless I'm mistaken).

There's intended to be some decoupling between infra and template code. An application or service can often be deployed in multiple ways (containers, functions, servers, k8s clusters, etc.). So an app only needs to be concerned with building, testing, linting, etc. The only real point of contact with infra is 1) e2e testing in deployed environments and 2) fetching any secrets needed to connect to remote resources. Consequently, we can say that infra as a whole can be owned by a premise repo instead of individual templates. We can simply expect all infra to live in the infra dir for now, tho it may make more sense for components to live at the top level and for app specific infra to be colocated... Needs some thought, and its own future ADR.

Finaly template authors will be able to generate migrations with `premise migration new`, which will simply generate the a migration for the next predicted version. This will be a text or yaml based file to power an LLM based workflow, also a topic for a future ADR.

## Positive Consequences

- Low learning curve, you get to use tools like mise reknown for DX
- Dev env is standardized, and managed easily in a declarative fashion via mise
- CI works the same was as local dev, and agents can be forced to do the same
- You just have fairly static templates which can EVOLVE, without complex tooling by leveraging LLMs
- Template residence for IaC modules and tasks (via mise remote tasks) can peevent LLMs from messing with things they should not be messing with, and keep them focused on application code
- Trunk based development and IaC from Day 0

## Negative Consequences

- Premise templates are NOT composable, so you may find yourself maintaining more templates than necessary
- Convention enforcement may be limited, users may find themselves in weird configurations which are unsupported by premise
- Have to know _some_ IaC from Day 0

## Open Questions

- Does infra updates even need to be premise owned? It's nice for it to not be alterable via LLMs...
- Should we introduce some element of conditional templating... which db to use etc... Could be done... But also standalone templating and some plumbing doesn't seem too bad either...
- infra dir per app, or one infra dir at the top level?
- data migration ordering...?
- secret management?
- provider setup/auth/validation?

## Links

- [mise monorepo tasks](https://mise.jdx.dev/tasks/monorepo.html), the layering premise builds on
- [Trunk Based Development](https://trunkbaseddevelopment.com/), the branching rules above
- [Pulumi stacks](https://www.pulumi.com/docs/iac/concepts/stacks/), the environment-to-stack mapping
- [Copier updating](https://copier.readthedocs.io/en/latest/updating/), the three-way migration mechanism
