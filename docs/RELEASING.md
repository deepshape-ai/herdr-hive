# GitHub Actions releases

Normal branch pushes and pull requests run `.github/workflows/ci.yml`: independent
macOS/Linux checks for both Go modules, followed by a Linux native Herdr integration
test. CI downloads the official Herdr 0.9.0 binary and verifies its pinned SHA-256.
All test keys are generated on the runner and removed; no internal infrastructure
or test credentials are required.

Publish one component by pushing its release tag after its changes are committed:

```sh
git tag hive/v0.1.0
git push origin hive/v0.1.0
# Bee is released independently:
git tag bee/v0.1.0
git push origin bee/v0.1.0
```

Tags ending in a prerelease suffix, such as `bee/v0.2.0-rc.1`, create prereleases.
The release workflow validates the tag, reuses the same CI checks at the tagged
commit, and packages only the selected component for four targets:

- Linux amd64 and arm64
- macOS amd64 and arm64

Every archive includes the executable, README and MIT license. Bee also includes
its Herdr manifest with the exact release version. `SHA256SUMS` covers all four
archives. Actions are pinned to commit SHAs. Only the final publishing job receives
`contents: write`; the repository's built-in `GITHUB_TOKEN` is sufficient. No PAT,
SSH key or test-host secret is needed.

The workflow requires the tag to exist and does not overwrite existing releases.
Both products share GitHub's release list, so releases deliberately do not claim
the repository-wide `latest` designation. Choose the desired component's tag.
The workflow uses `docs/releases/COMPONENT-VERSION.md` when present; otherwise it
generates repository-wide GitHub release notes. Add component-specific notes
before pushing a tag.

Failed checks or incomplete artifact sets prevent publication. A rerun can retry
a failed build before publishing; replacing an already published artifact requires
an explicit maintainer decision and is not automated. Users upgrade with `bee update` or `hive update --state-dir PATH`.

Workflows run when the corresponding GitHub events occur. Creating a workflow
file alone does not push code or publish a release. macOS archives are not
notarized; this pipeline does not use signing credentials.

The updater lists releases by component tag and selects the highest stable semantic
version; it does not use GitHub's repository-wide latest release. Drafts and
prereleases are excluded. A release must include the exact archive names above
and `SHA256SUMS`. These names are an installed-client compatibility contract.
Keep the shared updater module in source checkouts when building either product.
