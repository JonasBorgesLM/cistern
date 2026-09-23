# Releasing

> **Nothing has been released yet.** The release workflow (`release.yml`) is
> delivered in phase C8; until then this document is the procedure it must
> implement.

Modules are versioned and tagged independently (ADR-0001, RNF-10). The tag
prefix selects the module.

| Module | Tag | Example |
| --- | --- | --- |
| `.` (core) | `vX.Y.Z` | `v0.1.0` |
| `redisstore/` | `redisstore/vX.Y.Z` | `redisstore/v0.1.0` |
| `examples/` | never tagged | — |

---

## Why order matters

`redisstore` will require the core. Until the core version it names is
published, that requirement resolves to nothing for anyone outside this
repository: a local `go.work` makes every local signal green, and a consumer
has no workspace. Tagging `redisstore` first publishes a release nobody can
`go get` — which is exactly how `moat`'s `redisstore/v0.2.0` shipped broken.

### The sequence

```
1. tag + push the core      →  the release job publishes it
2. wait for the proxy       →  go list -m github.com/JonasBorgesLM/cistern@vX.Y.Z
3. bump redisstore/go.mod   →  require the core at the version just published
4. let CI go green          →  satellite-resolution now proves the real pin
5. tag + push redisstore
```

## Before the first tag of a module

- **The public API freezes.** Walk `go doc -all ./...` and ask of each exported
  name whether it is the shape to keep.
- **Tags are signed (SSH)** and verified against `.github/allowed_signers`
  (RNF-10). Rehearse once:

  ```bash
  git tag -s v0.0.1-rehearsal -m "rehearsal"
  git config gpg.ssh.allowedSignersFile .github/allowed_signers
  git verify-tag v0.0.1-rehearsal
  git tag -d v0.0.1-rehearsal
  ```

- **Private vulnerability reporting is on** (RNF-09). It was found disabled
  during both the `moat` and `crier` releases, so it is a checklist item:

  ```bash
  gh api repos/JonasBorgesLM/cistern/private-vulnerability-reporting
  # {"enabled":true}
  ```

- **The physical key format and the envelope version freeze with
  `redisstore`.** After that, changing either is a migration, not an edit
  (`REQUIREMENTS.md` §8.6, ADR-0009 planned).
- **The audit is done in a clean session**, by someone who did not write the
  code (`REQUIREMENTS.md` §15).

## Which branch

Tags are cut on `main`, and every release commit is made there; `main` is then
merged back into `develop` (see [`CONTRIBUTING.md`](CONTRIBUTING.md#develop-and-main)).

## Release notes

There is no committed `CHANGELOG.md`. Release notes are generated at release
time from the real API diff (`gorelease` against the module's previous tag) and
published as the GitHub Release for the tag (RNF-10). Hot-path benchmark
regressions (RNF-06) are called out there.

## If a release is wrong

**Do not delete or move the tag.** The module proxy has cached it permanently.
Cut the next patch, and `retract` the bad version in `go.mod` if it must not be
used.
