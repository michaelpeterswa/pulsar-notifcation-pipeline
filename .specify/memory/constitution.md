<!--
Sync Impact Report
==================
Version change: TEMPLATE (unversioned) → 1.0.0
Bump rationale: Initial ratification of a concrete constitution from the placeholder
  template. MAJOR because this establishes the first binding principle set; there
  are no prior ratified rules to compare against.

Modified principles (old title → new title):
  - [PRINCIPLE_1_NAME] → I. Interface-First Design
  - [PRINCIPLE_2_NAME] → II. Functional Options Pattern (NON-NEGOTIABLE)
  - [PRINCIPLE_3_NAME] → III. Writer / Reader-Deliverer Separation
  - [PRINCIPLE_4_NAME] → IV. Test Discipline (Unit + Testcontainers)
  - [PRINCIPLE_5_NAME] → V. Observability & Environment-Driven Configuration

Added sections:
  - Additional Constraints (Go Toolchain, Messaging Substrate, Layout)
  - Development Workflow (CI gates, commits, reviews)
  - Governance

Removed sections: none (all template placeholders replaced)

Templates requiring updates:
  - ✅ .specify/memory/constitution.md (this file)
  - ⚠ .specify/templates/plan-template.md — "Constitution Check" gate is a
    placeholder; individual feature plans must enumerate the five principles
    as explicit gates (no structural change to template required).
  - ⚠ .specify/templates/spec-template.md — no change required; specs remain
    user-centric and technology-agnostic by design.
  - ⚠ .specify/templates/tasks-template.md — no structural change required;
    "Tests are OPTIONAL" language stays permissible at the template level, but
    per Principle IV, generated task lists for this project MUST include test
    tasks (unit and/or testcontainers) for every behaviour-bearing task.
  - ⚠ README.md — still describes the upstream `go-start` template; rewrite
    when the pipeline implementation begins (out of scope for this amendment).

Deferred TODOs: none. RATIFICATION_DATE set to today (2026-04-23) as this is
the initial adoption.
-->

# Pulsar Notification Pipeline Constitution

## Core Principles

### I. Interface-First Design

Every feature capability MUST be expressed as a Go `interface` in a package the
consumers import; concrete types are provided as one implementation behind that
interface. Consumers MUST depend on the interface, never on the concrete struct.
New implementations (alternate brokers, alternate sinks, alternate stores) MUST
be droppable without edits to call sites beyond wiring.

**Rationale**: The pipeline is split across a writer and a reader/deliverer and
is expected to evolve — new delivery channels, new stores, new brokers. Coding
to interfaces keeps those seams open and makes every dependency substitutable
for tests (see Principle IV).

### II. Functional Options Pattern (NON-NEGOTIABLE)

All exported structs that accept configuration MUST be constructed via a
`New...(required, opts ...Option) (*T, error)` function using the functional
options pattern. Options MUST be typed as `type Option func(*T)` (or an
equivalent options struct modifier) with `With<Name>` helpers. Public structs
MUST NOT be zero-value-constructed by callers, and constructors MUST NOT grow
positional parameters beyond the minimum required identifiers.

**Rationale**: Consistency of construction across the codebase is a hard
requirement for readability and evolvability. The functional options pattern
lets us add configuration knobs without breaking callers and without sprawling
builder objects. This rule is non-negotiable because inconsistent construction
styles are expensive to undo once the surface grows.

### III. Writer / Reader-Deliverer Separation

The project ships exactly two runnable applications: a **writer** that accepts
notification intents and publishes them to the messaging substrate, and a
**reader/deliverer** that consumes from the substrate and performs delivery.
They MUST NOT share in-process state and MUST NOT import each other's internal
packages. Any type crossing the wire MUST live in a shared contract package
(e.g., `internal/contracts` or equivalent) and be versioned deliberately.

**Rationale**: A clean writer/reader boundary is the architectural thesis of
the project. Enforcing it at the package level prevents creeping coupling that
would defeat the horizontal scalability the two-process split is there to
provide.

### IV. Test Discipline (Unit + Testcontainers)

Every exported function, method, and interface implementation MUST have tests.
Pure logic MUST be covered by standard `go test` unit tests. Code paths that
interact with external systems (Apache Pulsar, databases, HTTP APIs, anything
outside the Go runtime) MUST be covered by integration tests using
`testcontainers-go` against real dependencies. Mocks and fakes are permitted
only at interface boundaries for driving unit tests of adjacent logic; they
MUST NOT be the sole verification of integration-critical code paths.

**Rationale**: The pipeline's correctness is defined by how it behaves against
a real broker, not against a mock. Testcontainers gives us production-shaped
verification without staging environments. Retaining unit tests for pure logic
keeps the fast feedback loop usable during development.

### V. Observability & Environment-Driven Configuration

Both applications MUST emit structured logs via `log/slog`, MUST expose
OpenTelemetry metrics and traces, and MUST be configured exclusively through
environment variables parsed via `github.com/caarlos0/env` (or successor).
Hard-coded configuration, ad-hoc `os.Getenv` reads outside a central config
struct, and `fmt.Println`-style logging are prohibited in application code.

**Rationale**: Operability in production is a design constraint, not an
afterthought. A single config loading path and a single logging/telemetry
shape across both apps is what makes a two-service system diagnosable.

## Additional Constraints

- **Go toolchain**: Target the Go version declared in `go.mod`; upgrades are
  governance changes (see below). `go vet` and `golangci-lint` MUST pass on
  every PR.
- **Messaging substrate**: The primary substrate is Apache Pulsar. Any
  substrate-specific code MUST sit behind an interface defined in Principle I
  so that the substrate remains substitutable for tests and future evolution.
- **Repository layout**: Runnable binaries live under `cmd/<app>/`; shared
  non-exported code lives under `internal/`; exported reusable packages (if
  any) live at the module root or under `pkg/`. Tests live next to the code
  they test (`_test.go`), with integration tests guarded by a build tag or
  `testing.Short()` skip so unit suites stay fast.
- **Dependency hygiene**: New direct dependencies require a line in the PR
  description justifying why a standard-library or existing-dependency
  solution is insufficient.

## Development Workflow

- **Commits**: Conventional Commits, enforced by `commitlint`.
- **Linting**: `golangci-lint`, `yamllint`, and `hadolint` MUST pass in CI.
- **Tests**: `go test ./...` MUST pass; integration tests using testcontainers
  MUST run in CI for changes that touch external-dependency code paths.
- **Code review**: Every PR MUST be reviewed against the five core principles.
  A reviewer rejecting a PR for a principle violation MUST cite the principle
  by number; the author MUST either bring the change into compliance or file
  a constitution amendment (see Governance) before merging.
- **Feature planning**: Plans produced by `/speckit.plan` MUST include a
  "Constitution Check" that enumerates each of the five principles and states
  either "complies" or "justified deviation (see Complexity Tracking)".

## Governance

This constitution supersedes ad-hoc conventions and prior informal practices.
When code and constitution disagree, the constitution wins and the code is
brought into compliance.

**Amendment procedure**: Amendments are proposed via a PR that modifies this
file, updates the Sync Impact Report comment at the top, and (when principles
change) updates any dependent templates under `.specify/templates/`. Amendments
merge only with explicit maintainer approval.

**Versioning policy**: Semantic versioning applies to this document.
  - MAJOR: a principle is removed, inverted, or redefined in a way that
    invalidates prior compliance.
  - MINOR: a new principle or section is added, or an existing principle is
    materially expanded.
  - PATCH: clarifications, wording, typo fixes, or non-semantic refinements.

**Compliance review**: Maintainers MUST periodically audit the codebase
against this document. Drift that is discovered but not fixable immediately
MUST be tracked as an issue tagged `constitution-drift`.

**Runtime guidance**: Day-to-day contributor guidance (commands, layout,
conventions) lives in `README.md` and in any agent-specific guidance files
under `.claude/` or similar. Those documents MUST defer to this constitution
on matters of principle.

**Version**: 1.0.0 | **Ratified**: 2026-04-23 | **Last Amended**: 2026-04-23
