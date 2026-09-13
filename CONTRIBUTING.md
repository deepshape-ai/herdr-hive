# Contributing

Hive and Bee are independent Go modules. Do not import one component's internal
packages from the other. Update the versioned wire contract and compatibility
fixtures when a change crosses that boundary.

Run `make test vet` and build the affected component. Native integration tests
are described in `tests/integration/README.md`; they create isolated sessions and
must never use existing employee sessions. Changes to SSH bootstrap must include
native Herdr verification and rejection tests for unsupported commands. Changes
to stream ownership must include disconnect and backpressure tests.

Never commit test credentials, real infrastructure addresses, generated binaries,
terminal captures or local configuration. `.local/`, `.r/` and `dist/` are ignored.
Use reserved example domains in documentation. Keep dependency lock files in Git.

Release Hive and Bee separately, using tags `hive/vX.Y.Z` and `bee/vX.Y.Z`.
A protocol change must either remain compatible with v1 or explicitly reject it;
unknown configuration fields/rules must not silently expand sharing.
