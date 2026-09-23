# Releasing

> **Nothing has been released yet.** [`release.yml`](.github/workflows/release.yml)
> implements the checks below.

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

- **The physical key schema (`v1`, ADR-0008) and the envelope version freeze
  with `redisstore`.** Changing either later loses no data — this is a cache —
  but every replica starts cold and mixed-version replicas stop sharing entries
  during the rollout, which lands on the source of truth. It needs a release
  note, not only a version bump.
- **The audit is done in a clean session**, by someone who did not write the
  code (`REQUIREMENTS.md` §15).

## Which branch

Tags are cut on `main`, and every release commit is made there; `main` is then
merged back into `develop` (see [`CONTRIBUTING.md`](CONTRIBUTING.md#develop-and-main)).

## What the release workflow checks

Everything that can fail is checked before the GitHub Release exists, because
a tag is immutable once anyone has fetched it. For the tagged module, with no
workspace — the resolution a consumer gets:

- the tag maps to a module, and every module in the tree has a tag pattern or
  a recorded exclusion (`examples` is never published);
- `go mod verify`, build, vet and `-race` tests **at the module's own floor**,
  with `GOTOOLCHAIN=local`, so the notes' claim about the floor is true;
- for `redisstore`: the integration suite against a real Redis, a core
  requirement that is a **released version, not a pseudo-version**
  ([`released-version.sh`](.github/scripts/released-version.sh)), and **no
  `replace` directive** ([`no-replace.sh`](.github/scripts/no-replace.sh)) —
  a replace would let every check above pass against the local core while the
  version it names does not exist (#116);
- for the core: still no dependency at all (ADR-0001);
- the documentation checks, and `govulncheck` on the newest toolchain;
- the API diff against this module's previous tag, read from local history;
- the tagged commit is on `main`, and the tag's SSH signature verifies against
  `.github/allowed_signers` **as it is on `main`**, not as it is in the tagged
  tree — otherwise a tag on an unreviewed commit that adds its own key would
  vouch for itself (#115). A file with no key fails closed.

What these checks do not cover: a tag push runs the `release.yml` of the
tagged commit, so a tag whose commit also rewrites the workflow skips them;
and the module proxy serves any pushed tag, whether or not the GitHub Release
was created. They catch a mistake and an unreviewed signer; they do not stop
someone with tag-push rights. That is a repository tag ruleset restricting
who can create `v*` and `redisstore/v*` tags.

## Release notes

There is no committed `CHANGELOG.md`. Release notes are generated at release
time from the real API diff (`gorelease` against the module's previous tag) and
published as the GitHub Release for the tag (RNF-10). Hot-path benchmark
regressions (RNF-06) are called out there.

## If a release is wrong

**Do not delete or move the tag.** The module proxy has cached it permanently.
Cut the next patch, and `retract` the bad version in `go.mod` if it must not be
used.
