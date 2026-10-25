# Contributing

This checkout is the `v0.3.0` line: Cage 0.3.0 with wlroots 0.20.2. The `v0.2.0` and `v0.1.5` branches have their own established build workflows; preserve those branch-specific workflows when working here.

## Updating Cage or wlroots

Cage and wlroots are pinned as submodules under `forks/`. Their development branches are recorded in `.gitmodules`. Make and publish a dependency change in this order:

1. Switch the dependency submodule to its recorded development branch, edit it, and commit the change there.
2. Push that fork commit.
3. Update and commit the parent repository gitlink with `git add forks/cage` or `git add forks/wlroots-xtmapper`.
4. Build only after the fork commit and parent gitlink are recorded.

The build stages committed submodule trees under ignored `build/source/`; Meson state is separate under `build/meson/`. Do not regenerate exported source patches. Dependency archives, Meson wraps, and `SHA256SUMS` are maintained inputs.

## Backports

When a fix also applies to `v0.2.0` or `v0.1.5`, make a separate backport commit on that branch and adapt it to that branch's existing source and build layout. Do not assume the v0.3.0 submodule pins, CI steps, dependency inputs, or installer apply there. Run that branch's documented checks and preserve any contributor attribution carried by the original change.

## Checks and pull requests

For build, launcher, or installer changes, run:

```sh
for script in build.sh install.sh install_deps.sh cage_xtmapper.sh tests/*.sh; do bash -n "$script"; done
shellcheck build.sh install.sh install_deps.sh cage_xtmapper.sh tests/*.sh
bash tests/test-install-deps.sh
bash tests/test-build.sh
bash tests/test-launcher.sh
./build.sh --debug
test -z "$(gofmt -l cmd internal tools tests)"
export CC=clang PKG_CONFIG_PATH="$PWD/build/native/pkgconfig"
go vet ./...
go test -race -count=1 ./...
```

Use `-count=1`: integration suites build or use a separate application, which Go's test-result cache does not track reliably. `tests/integration/install/` uses real filesystem operations and processes under private temporary home-directory prefixes; its basic archive fixtures contain an inert host ELF. Runtime-lock cases use the built project ELF with a real headless compositor and mock Android tools. They require an unprivileged test user. See [docs/installation.md](docs/installation.md) for the journal/ownership and lock contracts.

The shell launcher remains a behavior reference for its tests. Installed and packaged applications use the single `cage-xtmapper` ELF. Keep installer tooling in the source checkout and preserve the release allowlist.

Pull requests should explain the user-visible behavior, list checks run, and include relevant dependency fork commits before the parent pin. Preserve prior contributor credit when adapting code; see [CONTRIBUTORS.md](CONTRIBUTORS.md).
