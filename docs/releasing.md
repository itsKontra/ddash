# Cutting a Release

A Release is one tag `vMAJOR.MINOR.PATCH` (a `-prerelease` suffix makes a
GitHub prerelease, which the core's newer-Release check skips). Pushing the
tag runs [`.github/workflows/release.yml`](../.github/workflows/release.yml),
which produces one artifact set:

- the core image `ghcr.io/itskontra/ddash:<tag>` (distroless/static, uid
  65532, port 8080, `ddash healthcheck` for `/healthz`);
- one GitHub release `<tag>` holding every Component binary (static
  `linux/amd64`, CGO off, stamped with the tag), every Plugin's bundle
  (`<plugin>.plugin.js` and `.css`), `catalogue.json` and `SHA256SUMS`.

## Steps

1. Start from a green `main`: CI passed on the commit you'll tag.
2. On a dev machine on the LAN, with a filled-in, gitignored `smoke.env`
   (copy [`smoke.env.example`](../smoke.env.example)), run

   ```sh
   make smoke
   ```

   It calls every system Component's getters against the real systems and
   prints `ok <component> <getter>` or `FAIL …` per line. On the first run
   nginx-config fails with its setup hint: add that authorized_keys line on
   the nginx host once (its key is kept in `.smoke/`).

   **Cut the Release only if it passes.** If it fails, fix the Component (and
   re-record its fixtures, one PR) first. `make smoke` never runs in CI.

   To re-record, fill in the `SCRUB_*` lines of `smoke.env` and a gitignored
   `scrub.map` (every other real name, `real = replacement` per line), then
   run `make record` (`SCENARIO=recorded` by default). It writes each answer,
   scrubbed, into `components/<name>/testdata/$(SCENARIO)/` and refuses any
   file that still holds a `scrub.map` name, the real domain or an address
   outside the scrubbed ranges. Review the files and move what the tests need
   into `baseline`; the IP guard in `go test` checks them again.
3. If this Release changes an Action, also run `make smoke-writes` (the
   reversible round trips on `DDASH_SMOKE` and `ddash-smoke`), and cut the
   Release only if it passes too.
4. Tag and push:

   ```sh
   git tag -a v1.2.3 -m v1.2.3
   git push origin v1.2.3
   ```

5. Watch the Release workflow. It runs `make check`, builds the Release dir
   (`make release-dir RELEASE=<tag> PLATFORM=linux/amd64`) and the image
   (`make image RELEASE=<tag>`), pushes the image, then creates the GitHub
   release. A failed run can be re-run as a whole until the GitHub release
   exists; the image push is idempotent.
6. Only once the GitHub release is there, deploy it: bump the image tag in
   the LXC's `compose.yaml` and `docker compose pull && docker compose up -d`
   ([deployment](deployment.md#updating)).

The first push creates the GHCR package private: once the repo is public, make
the package public too (Package settings), so the host pulls without a login.

## Building a Release locally

The same make targets build everything without publishing anything:

```sh
make release-dir RELEASE=v0.0.0-local PLATFORM=linux/amd64   # dist/
make image RELEASE=v0.0.0-local                                # the image (DOCKER=podman works)
docker run --rm ghcr.io/itskontra/ddash:v0.0.0-local version  # prints v0.0.0-local
```

To run that image against the local Release dir instead of GitHub, mount
`dist/` and set `DDASH_RELEASE_DIR` to it (the dir must be readable by uid
65532).
