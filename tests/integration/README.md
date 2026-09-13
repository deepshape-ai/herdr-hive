# Integration tests

Run from the repository root. The test account must be dedicated to testing.
Tests never attach to existing Herdr sessions or modify user SSH configuration.
They refuse to reuse `.r`, create disposable identities and sessions there, and
remove their temporary installation on exit. Do not run these scripts concurrently.

```sh
make package
python3 -m venv .local/venv
.local/venv/bin/pip install -r tests/integration/requirements.txt
.local/venv/bin/python tests/integration/native.py
```

Requires Herdr 0.9.0 and OpenSSH. The native test covers three local publishers,
real native `machine add`, real terminal input/output, name collisions, shell and
unshared-target rejection, and sharing shutdown without agent termination.
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

Latency samples measure input to a reconstructed ANSI terminal screen; this is
not physical display latency. Keep real host addresses, keys and raw terminal
captures out of recorded/public test results. The relay backpressure/capacity test
is implemented separately in `hive/internal/server/server_test.go` and runs under
the Go race detector through `make test`.
