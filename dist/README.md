# dist/ — ongrid release pipeline

This directory owns the **release/packaging** pipeline for ongrid. One command
produces one artefact, ready to scp to any Linux box with docker + docker
compose installed.

## What `make package` produces

A single tarball:

```
dist/out/ongrid-v<VERSION>-linux-amd64.tar.xz
dist/out/ongrid-v<VERSION>-linux-amd64.tar.xz.sha256
```

Unpacked layout:

```
ongrid-v<VERSION>-linux-amd64/
  VERSION
  README.md              (from deploy/install/README.md)
  install.sh             (from deploy/install/install.sh)
  uninstall.sh
  upgrade.sh
  docker-compose.yml     (prod compose, from deploy/install/)
  .env.example
  prometheus/
    prometheus.yml       (from deploy/prometheus/)
  images/
    ongrid.tar           (docker save ongrid:<VERSION>)
  edge/
    ongrid-edge-linux-amd64
    ongrid-edge-linux-arm64
    ongrid-edge-darwin-amd64
    ongrid-edge-darwin-arm64
    install-edge.sh
    ongrid-edge.yaml.example
    ongrid-edge.service
```

The cloud service ships as a docker image tarball (`images/ongrid.tar`);
`install.sh` runs `docker load -i images/ongrid.tar` and then
`docker compose up -d`. Edge agents ship as static binaries for four OS/arch
combos so users can run them directly on heterogeneous hosts.

## Release flow

1. Bump the version: edit `VERSION` at the repo root (e.g. `v0.1.1`).
2. Run `make package` from the repo root. This will:
   - `build-linux`       — cross-compile ongrid for linux/amd64
   - `build-edge-all`    — cross-compile ongrid-edge for 4 targets
   - `docker-build`      — build `ongrid:<VERSION>` image
   - stage everything under `dist/stage/ongrid-<VERSION>-linux-amd64/`
   - emit the tarball + sha256 under `dist/out/`
3. Ship: `scp dist/out/ongrid-v<VERSION>-linux-amd64.tar.xz user@host:~/`.
4. On the target: untar, `sudo ./install.sh`.

## Checksum

`dist/out/ongrid-v<VERSION>-linux-amd64.tar.xz.sha256` sits next to the
tarball. The install script can verify integrity with `sha256sum -c` on
Linux or `shasum -a 256 -c` on macOS.

## Local dry-run

Test the tarball without shipping:

```
make package
mkdir -p /tmp/ongrid-test && tar -xf dist/out/ongrid-v*.tar.xz -C /tmp/ongrid-test
cd /tmp/ongrid-test/ongrid-v*
ls -R
# Inside a disposable Ubuntu container with docker socket mounted:
#   docker run --rm -it -v $PWD:/pkg -v /var/run/docker.sock:/var/run/docker.sock \
#     ubuntu:22.04 bash -c 'cd /pkg && ./install.sh'
```

## Files in this directory

- `package.sh` — assembly script invoked by `make package`. Tolerates missing
  `deploy/install/*` files (warns, continues) so the pipeline is testable
  before the on-target scripts land.
- `README.md` — this file.

## What this directory does NOT own

- `deploy/install/**` — on-target install/uninstall/upgrade scripts and prod
  `docker-compose.yml`. Owned by the install-agent.
- `deploy/Dockerfile.*`, `deploy/docker-compose.yml` — build contexts and dev
  compose file.
- Images are **never** pushed to a registry from this pipeline. The tarball
  is the distribution channel.
