# Release policy

Releases are automatic when all required gates pass.

Version format:

```text
v<upstream-version>-patch.<counter>
```

Example:

```text
v0.1.136-patch.1
```

The counter increments for repeated releases on the same upstream version and resets when the
upstream version changes.

Required gates:

1. Upstream commit CI is complete and passing before mirror sync.
2. Patchset applies with three-way `git am`.
3. Backend checks pass.
4. Frontend typecheck and lint pass.
5. Reference sanitizer passes.
6. Docker build or release build passes.
7. No release tag with the computed version exists.

Release notes include only:

- upstream version
- upstream commit SHA
- patchset commit SHA
- patch topic list
- artifact references in this repository namespace
