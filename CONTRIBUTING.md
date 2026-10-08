# Contributing

## Setup

```sh
python3 -m venv .venv && .venv/bin/pip install ansible-core==2.21.5 ansible-lint
export PATH=$PWD/.venv/bin:$PATH
make build
```

## Before you push

```sh
make test        # Go unit tests
make lint        # go vet, golangci-lint, ansible-lint
make role-test   # fault roles against a throwaway node container (needs Docker)
promtool test rules examples/shop/rules_test.yaml
bin/drillbook lint --rules examples/shop/rules.yaml
```

If you changed a drill, a runbook or the drill environment, run the drill for real against `env/up.sh` and say in the pull request which verdict you got. CI runs the affected drills that fire within 20 minutes; slower ones run weekly.

## Tests

- Verdict logic, the drill engine, the runbook parser and anything else that decides pass or fail is written test first. These fail quietly and still print plausible output.
- Unit tests need no network and no cluster. The engine is tested against a simulated system in `internal/engine/engine_test.go`.
- Fault roles are tested by `tests/roles/run.sh`, including the case where nobody reverts and the host timer has to.

## Commits

- One logical change per commit. Tests pass at every commit.
- Subject line only, in the form `<type>: <description>`, with a 4 to 6 word description and no trailing period.
- Types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`.
- No trailers.
- Commit as each piece lands; do not batch a day's work into one commit.

Good: `feat: validate drill names`, `fix: keep silence until alert clears`
Bad: `update code`, `fix stuff`, `wip`

## Results

Never write a drill result into a document by hand. Copy it from `.drillbook/results.jsonl` with `drillbook report`, and name the commit the run used.
