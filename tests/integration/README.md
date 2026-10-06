# Integration tests

Run from the repository root. The test account must be dedicated to testing.
Tests never attach to existing Herdr sessions or modify user SSH configuration.
The full native acceptance test creates a short private directory under `/tmp`.
Other scripts use their own disposable test directories and refuse to reuse them.
All remove their temporary installations on exit. Do not run these scripts concurrently.

```sh
make package
python3 -m venv .local/venv
.local/venv/bin/pip install -r tests/integration/requirements.txt
.local/venv/bin/python tests/integration/native.py
python3 tests/integration/panel.py
```

Native integration requires compatible Herdr 0.9.0 or newer and OpenSSH.

CI runs the full native suite against checksum-pinned Herdr 0.9.0, 0.9.1, 0.9.2
and 0.9.3 on branch/PR/release builds. Weekly and manually dispatched CI runs
check the minimum version and the latest official stable release, requiring
GitHub's asset SHA-256 before executing it. A future release must still offer
generation 1 and the frozen required codecs; version numbers alone are not a
compatibility guarantee.

`python3 -B tests/integration/remote_cli.py` independently verifies the CLI API
route using three disposable Bees, real Hive/Herdr binaries and a deterministic
foreground agent fixture. It covers A→B, B→C and C→A native agent creation,
prompt/wait/read, same-name isolation, concurrent B/C calls, tab/pane management,
explicit pane focus and rejection of `--current` without moving the owner's focus,
native exit codes, private-target rejection and unsharing. The agent fixture
uses real Herdr reporting/detection but makes no paid inference requests and
needs no agent account. All sessions, keys, binaries and sockets live under a
temporary directory and are removed. No user installation is changed.
Set `BEE_NATIVE_TEST_BINARY` and `HIVE_NATIVE_TEST_BINARY` to absolute executable
paths to run the same acceptance test against downloaded release binaries.
It also stops and restarts a publishing Herdr server twice, verifying stale
publication withdrawal, automatic rebinding, stable share IDs, new generations
and native CLI access without toggling Bee sharing.

The native UI test covers three local publishers,
real native `machine add`, real terminal input/output, name collisions, shell and
unshared-target rejection, and sharing shutdown without agent termination.
The test also measures actual PTY sizes with a publisher-local shell and two
remote viewers: fixed sizes across focus/input/resize, independent tabs, remote
tab switches, pane closure, last-viewer release and existing-controller conflicts.
`BEE_NATIVE_TEST_BINARY=/absolute/path/to/bee` substitutes an installed Bee binary
for the publisher under test, while retaining disposable sessions and identities.
Terminal output assertions reconstruct ANSI screen updates rather than requiring
complete markers in raw incremental byte chunks.
An independent full/patch decoder checks sustained scrolling output with modern
optional hello flags, continuous source visibility, complete source-switch replay,
and upstream stream accounting/release. Hive deliberately negotiates only its
implemented frozen codec, even when a viewer offers newer optional encodings.

An isolated OpenSSH config wrapper only redirects config lookup; it does not
replace Herdr, its protocol, or the SSH executable.

## Two-host LAN test

Build `dist/hive-linux-amd64` and `dist/bee-linux-amd64`, and place an official,
checksum-verified Herdr 0.9.0 Linux binary at
`.local/downloads/herdr-linux-x86_64`. The remote host must provide Linux x86-64,
Python 3, OpenSSH and `setsid`, and permit the newly selected test TCP port.

```sh
(cd hive && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/hive-linux-amd64 ./cmd/hive)
(cd bee && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/bee-linux-amd64 ./cmd/bee)
HIVE_TEST_TARGET=test-user@test-host.example.internal \
  .local/venv/bin/python tests/integration/lan.py
```

Authentication belongs to OpenSSH. An existing private control socket can be
supplied via `HIVE_TEST_CONTROL`. Never add test credentials or real host names to
this repository. The script installs all three binaries in a new remote temporary
directory, starts isolated sessions, verifies native access in both directions,
self-share exclusion, stable reconnection after Hive restart, and cleans up.

The separate `panel.py` test creates an isolated local Herdr server and Bee plugin
under `.local/panel-native`. It checks native split/open/focus/close/reopen,
automatic refresh after CLI edits, and keyboard navigation, then removes its
processes and files. It requires only Python's standard library, Go and Herdr.
`go -C bee test ./internal/app` also covers stale mouse targets, draft merging,
paste handling and light/dark layouts at normal and compact terminal sizes.

Latency samples measure input to a reconstructed ANSI terminal screen; this is
not physical display latency. Keep real host addresses, keys and raw terminal
captures out of recorded/public test results. The relay backpressure/capacity test
is implemented separately in `hive/internal/server/server_test.go` and runs under
the Go race detector through `make test`.

Invitation onboarding is covered by `native.py`: an isolated new device consumes an
invitation issued by the real Hive CLI, resumes after interrupted machine setup
and token revocation, joins an empty Hive, reuses its key and machine, and enables
a previously disabled machine, reuses an equivalent manually configured Hive,
and exercises pasting and clicking Join in a real terminal. The fixture redirects the SSH home lookup and
OpenSSH config to temporary files while using the real Herdr CLI and SSH transport.
Panel acceptance starts on the invitation view; unit tests verify masking and
submission plus malformed invitations, trust changes and configuration preservation.

To run only the enrollment/join acceptance (including the actual panel click), use
`.local/venv/bin/python tests/integration/native.py --onboarding-only`.
