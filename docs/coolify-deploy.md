# Coolify production deployment

This is an operator runbook, not a record of a deployment. Writing this document did **not** deploy an image, change production configuration, migrate production data, or create/reset production credentials. Production inspection was read-only. Schedule an explicit maintenance window before executing the mutating steps; this is **not** a zero-downtime SQLite rollout.

## Scope and observed configuration

Observations are a point-in-time snapshot, not an immutable configuration guarantee. Recheck them before proceeding.

| Setting | Observed production value |
| --- | --- |
| Coolify | <https://host.barnlab.dev>, v4.1.2 |
| Application | Barnlab / OregonDevFoundry (prod) |
| Production UUID | `jm2bmpkxkgxrnhnmxvvdant6` |
| Test UUID — **not production** | `c4d3qe5tu9t5epxkl8wu99cb` |
| Build pack / image | `dockerimage` / `ghcr.io/bkroeze/oregon-dev-foundry:latest` |
| Port | `8080` |
| Database | `DATABASE_PATH=/storage/data/odf_prod.db` |
| Persistent bind | host `/data/coolify/applications/odf-prod` → container `/storage`, read/write |
| Runtime UID:GID | `10001:10001` |
| Filesystem | `/storage` is root-owned, mode `0755`; `/storage/data` is `10001:10001`, mode `0755`; DB mode `0644` |
| Current optional settings | `MEDIA_DIR` and `SERVICES_ENABLED` unset |
| Health | Public `https://oregondevfoundry.com/healthz` returned `ok`; Coolify health-check setting disabled |

The image has a `/healthz` endpoint/health check, but that does not mean Coolify's health-check setting is enabled. The placeholder `git_repository=coollabsio/coolify` is irrelevant to this Docker-image application.

The observed image digest was `sha256:18602f1c36bd1cb6d7a7f1a4d2314a8e2d6f3f0d96d61bbef6d2c7aaa3a04ebd`. **Do not use this observation as the rollback selection:** capture the actual running image ID and repository digest immediately before rollout.

`/data` inside the container is a separate anonymous volume. Do not switch production to image-default database/media paths or put data there. Keep the existing `/storage` bind; adding a new volume over it could hide the production database.

Go serves `/media/products` directly. There is no Nginx/static-hosting directory to populate. Uploads belong outside the image, in the persistent bind.

## 1. Prepare a unique release image locally

These are instructions for a subsequent authorized release; no push is performed by preparing this document.

1. Review the intended source and authored template changes. Regenerate templates, run the repository checks, and review the generated changes before committing. For example, from the repository root:

   ```bash
   go run github.com/a-h/templ/cmd/templ@v0.3.977 generate
   go test ./...
   go vet ./...
   git diff
   ```

   Alternatively use the repository's `just check` workflow, then review any generated changes. Commit the intended code **and generated templates**; do not accidentally include credentials, local databases, or uploads.
2. Build from that reviewed commit, with a clean intended release tree. Use a unique tag, never overwrite an existing release tag:

   ```bash
   REV=$(git rev-parse HEAD)
   RELEASE_IMAGE="ghcr.io/bkroeze/oregon-dev-foundry:release-$REV"
   docker build --platform linux/amd64 -t "$RELEASE_IMAGE" .
   docker push "$RELEASE_IMAGE"
   ```

   Use the existing Docker registry credential/login mechanism. Never pass secrets as Docker build arguments or bake runtime environment files into the image. If this tag already exists, reuse its verified artifact or make a new commit/tag; do not replace it.
3. Record the pushed image digest and release tag privately with the deployment record. Ensure Coolify can pull that release before entering downtime.

## 2. Host preflight and private rollback record

Use a trusted interactive terminal and SSH session:

```bash
ssh bruce@host.barnlab.dev
bash
set +x
set -euo pipefail
umask 077
APP_UUID=jm2bmpkxkgxrnhnmxvvdant6
mapfile -t CIDS < <(docker ps -q --filter "name=${APP_UUID}-")
if (( ${#CIDS[@]} != 1 )); then
  printf 'Expected exactly one running production container; found %s. Stop and investigate.\n' "${#CIDS[@]}" >&2
  exit 1
fi
CID=${CIDS[0]}
OLD_IMAGE=$(docker inspect --format '{{.Image}}' "$CID")
BACKUP_DIR="$HOME/odf-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$HOME/odf-backups"
mkdir -m 0700 "$BACKUP_DIR"
printf '%s\n' "$CID" > "$BACKUP_DIR/old-container-id"
printf '%s\n' "$OLD_IMAGE" > "$BACKUP_DIR/old-image-id"
docker image inspect --format '{{json .RepoDigests}}' "$OLD_IMAGE" > "$BACKUP_DIR/old-repo-digests.json"
docker inspect "$CID" > "$BACKUP_DIR/old-container-inspect.json"
docker inspect --format '{{range .Mounts}}{{println .Type .Source "->" .Destination .RW}}{{end}}' "$CID"
docker exec "$CID" id
docker exec "$CID" sh -ec 'test "$DATABASE_PATH" = /storage/data/odf_prod.db; stat -c "%u:%g %a %n" /storage /storage/data /storage/data/odf_prod.db'
command -v sqlite3
read -r -p 'Full new release image (ghcr.io/bkroeze/oregon-dev-foundry:release-...): ' RELEASE_IMAGE
[[ "$RELEASE_IMAGE" == ghcr.io/bkroeze/oregon-dev-foundry:release-* ]]
docker pull "$RELEASE_IMAGE"
```

The mount listing must show the expected writable production bind. Stop if identity, paths, or permissions differ. This host has Docker and host `sqlite3`; privileged filesystem operations require an interactive sudo password. The runtime image has neither Bash, `just`, Go, nor `sqlite3`; run the operator shell on the **host**.

The private inspect file contains secrets. Do not print it, paste it into chat/issues, commit it, or put it under a publicly served path. Also save the current **Coolify** configuration/environment privately in this `0700` backup directory (a protected editor/export), including image reference, storage source/destination, domains, port, health settings, and whether each optional variable is absent versus explicitly set. Runtime inspect is useful but is not a complete Coolify configuration backup. Preserve existing mail, Stripe, Turnstile, and other secrets unchanged.

Keep the old local image and a pullable old repository digest available; do not prune them. If no repository digest is recorded, establish a recoverable pinned old image before proceeding rather than assuming mutable `latest` will still identify it.

## 3. Enter maintenance and take a cold backup

Pause automatic deployments, scheduled restarts, and any automation that could start another application instance. Arrange maintenance routing/block incoming writes, and allow requests to drain. If downtime has not been explicitly accepted, stop here and schedule it.

There must be **one SQLite writer**: no old/new rolling overlap and no other container/process sharing this database. Keep automation paused through verification. Stop, but do not remove, the selected container so its mount remains available:

```bash
docker stop -t 30 "$CID"
test "$(docker inspect --format '{{.State.Running}}' "$CID")" = false
docker run --rm --user 0:0 --volumes-from "$CID:ro" \
  --entrypoint tar "$OLD_IMAGE" -C /storage -cpf - . \
  > "$BACKUP_DIR/storage.tar"
chmod 0600 "$BACKUP_DIR/storage.tar"
test -s "$BACKUP_DIR/storage.tar"
tar -tf "$BACKUP_DIR/storage.tar" > "$BACKUP_DIR/storage-contents.txt"
sha256sum "$BACKUP_DIR/storage.tar" > "$BACKUP_DIR/storage.tar.sha256"
mkdir -m 0700 "$BACKUP_DIR/backup-check"
tar --no-same-owner -xpf "$BACKUP_DIR/storage.tar" -C "$BACKUP_DIR/backup-check"
sqlite3 -readonly "$BACKUP_DIR/backup-check/data/odf_prod.db" 'PRAGMA integrity_check;'
```

Continue only if the integrity check returns exactly `ok`. The host shell's `set -e` stops this sequence on command failure; leave the app stopped and investigate any archive/integrity failure. The separate inspection extraction uses `--no-same-owner` so the host operator owns the copied DB and journals. Never inspect the live database in this step, and do not use `immutable=1` (it can ignore WAL). The actual rollback extraction below preserves numeric ownership.

Archive **all of `/storage`**, including any SQLite `-wal`/`-shm` files and media. Never copy only the live main SQLite file: committed state can still be in WAL. Retain the cold archive securely off-host according to your backup policy before treating it as disaster recovery.

## 4. Provision persistent media and deploy the same app

Create the media directory through the stopped container's existing mount, as the application user, not with `chmod 777`:

```bash
docker run --rm --user 10001:10001 --volumes-from "$CID" \
  --entrypoint sh "$OLD_IMAGE" -ec \
  'mkdir -p /storage/data/media; test -w /storage/data/media; stat -c "%u:%g %a %n" /storage/data/media'
```

In the **existing production application** `jm2bmpkxkgxrnhnmxvvdant6` in Coolify:

- Keep host `/data/coolify/applications/odf-prod` mounted read/write at `/storage`.
- Set runtime variables (not build secrets):

  ```text
  DATABASE_PATH=/storage/data/odf_prod.db
  MEDIA_DIR=/storage/data/media
  SERVICES_ENABLED=true
  SESSION_COOKIE_SECURE=true
  PORT=8080
  ```

- Preserve all other existing variables/secrets, domains, and the exposed port `8080`.
- Set **Docker Image** to `ghcr.io/bkroeze/oregon-dev-foundry` and **Tag** to the unique `release-<git-revision>` prepared above (not `latest`). Record the pulled digest with the release; use digest pinning if supported by the installed Coolify image-reference controls.
- Save the storage/env/image settings together before deploying. Deploy with the old container **stopped**. Do not start the old container alongside the replacement or allow a rolling duplicate to share SQLite.

Once Coolify reports the replacement is running, in the same host Bash session capture its actual ID:

```bash
mapfile -t CIDS < <(docker ps -q --filter "name=${APP_UUID}-")
if (( ${#CIDS[@]} != 1 )); then
  printf 'Expected exactly one production writer; found %s. Stop and investigate.\n' "${#CIDS[@]}" >&2
  exit 1
fi
CID=${CIDS[0]}
docker inspect --format '{{.Image}}' "$CID"
docker inspect --format '{{range .Mounts}}{{println .Type .Source "->" .Destination .RW}}{{end}}' "$CID"
docker exec "$CID" id
docker exec "$CID" sh -ec '
  test "$DATABASE_PATH" = /storage/data/odf_prod.db
  test "$MEDIA_DIR" = /storage/data/media
  test "$SERVICES_ENABLED" = true
  test "$SESSION_COOKIE_SECURE" = true
  test "$PORT" = 8080
  printf "DATABASE_PATH=%s\nMEDIA_DIR=%s\nSERVICES_ENABLED=%s\nSESSION_COOKIE_SECURE=%s\nPORT=%s\n" "$DATABASE_PATH" "$MEDIA_DIR" "$SERVICES_ENABLED" "$SESSION_COOKIE_SECURE" "$PORT"
  test "$(id -u)" = 10001
  test "$(id -g)" = 10001
  probe=$(mktemp "$MEDIA_DIR/.write-check.XXXXXX")
  rm "$probe"
  stat -c "%u:%g %a %n" "$MEDIA_DIR"
'
```

Verify the image ID corresponds to the selected release and the mount still points to the original production source. This prints only an environment allowlist; do not dump all environment variables into logs.

### Database migrations and catalog seed

Auth and commerce migrations run automatically at startup against `DATABASE_PATH`. There is no separate SQL migration command to run, and you must not copy a local development DB into production. Existing accounts/roles and orders remain in the existing database. This release adds the commerce image/seed tables `commerce_product_images` and `commerce_catalog_seeds`.

Signage seeding runs only with `SERVICES_ENABLED=true`, and only once. It preserves an existing signage slug and later admin changes. A newly seeded catalog enables the default 4×4 size and stand; other sizes start disabled. Zero inventory makes this an inquiry/preview-only flow, not a checkout-ready stock configuration.

A seed failure can leave the process listening while every request, including `/healthz`, returns `500` with “application catalog is unavailable.” A running-container indicator is not proof of success: verify health, the actual public signage page, and authenticated catalog below, and investigate application logs privately if they fail.

## 5. Create or reset your chosen admin account

Run this **after backup and deployment**. Even the users CLI's list operation opens the store and runs migrations. Always give the production database path explicitly:

```bash
docker exec "$CID" /usr/local/bin/users list --database /storage/data/odf_prod.db
```

Choose your own username; this runbook intentionally contains no personal username or password. Inspect the account list, then explicitly choose **create** for a new account or **reset** for an existing account. Do not reset an account merely because a `show`/lookup command failed; diagnose the failure first.

Use the following in the trusted interactive **host Bash** session (including when connected through SSH). The runtime has `/usr/local/bin/users` but no Bash. Do not use terminal/session recording that captures passwords. Password entry is silent, is not a command argument, history entry, or exported environment variable, and is sent over stdin. There is no supported `ADMIN_PASSWORD` environment bootstrap.

```bash
(
  set +x
  set -euo pipefail
  unset PASSWORD CONFIRM
  trap 'unset PASSWORD CONFIRM' EXIT
  read -r -p 'Username: ' USERNAME
  test -n "$USERNAME" || { printf 'Username is required.\n' >&2; exit 1; }
  read -r -p 'Action (create/reset): ' ACTION
  case "$ACTION" in
    create)
      read -r -p 'Display name: ' DISPLAY_NAME
      test -n "$DISPLAY_NAME" || { printf 'Display name is required.\n' >&2; exit 1; }
      ;;
    reset) ;;
    *) printf 'Choose exactly create or reset.\n' >&2; exit 1 ;;
  esac
  read -r -s -p 'Password (12–72 bytes): ' PASSWORD
  printf '\n'
  read -r -s -p 'Confirm password: ' CONFIRM
  printf '\n'
  test "$PASSWORD" = "$CONFIRM" || { printf 'Passwords do not match.\n' >&2; exit 1; }
  test -n "$PASSWORD" || { printf 'Password must not be empty.\n' >&2; exit 1; }
  PASSWORD_BYTES=$(printf '%s' "$PASSWORD" | wc -c)
  if (( PASSWORD_BYTES < 12 || PASSWORD_BYTES > 72 )); then
    printf 'Password must be 12–72 bytes.\n' >&2
    exit 1
  fi
  if [[ "$ACTION" == create ]]; then
    printf '%s\n' "$PASSWORD" | docker exec -i "$CID" /usr/local/bin/users create \
      --database /storage/data/odf_prod.db --username "$USERNAME" \
      --display-name "$DISPLAY_NAME" --role admin --password-stdin
  else
    printf '%s\n' "$PASSWORD" | docker exec -i "$CID" /usr/local/bin/users update \
      --database /storage/data/odf_prod.db --username "$USERNAME" \
      --role admin --password-stdin
  fi
)
```

Use `docker exec -i`, **not `-t`**. The CLI/store enforces its existing password validation (minimum 12, bcrypt maximum 72 bytes). Reset intentionally promotes the selected account to admin, preserves unrelated fields, and revokes its existing sessions. Re-login with the chosen credentials, then open `/admin/catalog`.

## 6. Acceptance checks before ending maintenance

Run host/public checks without exposing credentials:

```bash
docker exec "$CID" sh -ec 'wget -qO- http://127.0.0.1:8080/healthz'
curl --fail --silent --show-error https://oregondevfoundry.com/healthz
curl --fail --silent --show-error --output /dev/null https://oregondevfoundry.com/shop/vending-signage
```

Both health responses should be `ok`. Also check in the browser:

- Log in as the chosen admin, open `/admin/catalog`, and confirm existing accounts, orders, and catalog records remain present where applicable.
- Upload real JPEG/PNG photos for the 4×4 size's **no-stand and stand** slots; for every additional enabled size, upload its own no-stand/stand pair. Exact signage combinations do not fall back to the product-default gallery image.
- Imports accept JPEG/PNG only, at most 8 MiB and 20 megapixels, and normalize to a maximum 1200-pixel edge without upscaling. Confirm imported images display on the public preview and their `/media/products/...` URLs return images.
- Verify files actually exist under host `/data/coolify/applications/odf-prod/data/media` (use interactive `sudo` if needed), not just in the container/image layer.
- Availability is independent of photos: enabling a size/stand option without photos must still show the enabled option with its no-photo state. Disable it again if it is not intended for launch. Do not enable paid inventory just to test the preview.
- Verify the title limit of 20 characters and URL limit of 200 characters. This is a preview/inquiry flow, not order submission. Check the default 4×4 and stand behavior and disabled options; existing admin changes should not be reset by the seed.
- With maintenance still in place, restart/redeploy the same pinned release, recapture the single running `CID`, and repeat health, login, catalog, and image checks. Uploaded files and admin credentials must persist.

Only after these pass, restore public traffic and deliberately resume the desired automation. Test-app experiments must use their **own** storage/database, never the production bind.

## 7. Rollback and recovery

Keep maintenance in place, pause automation, and stop the failed/new application before either rollback path. Recapture its single running production `CID` using the guarded selection above; do not accidentally stop the test app. Do not start the old container concurrently.

Before restoring anything, take a second **cold** archive of the failed deployment's entire `/storage` for forensic recovery and any new writes:

```bash
FAILED_IMAGE=$(docker inspect --format '{{.Image}}' "$CID")
FAILED_BACKUP_DIR="$HOME/odf-backups/failed-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 0700 "$FAILED_BACKUP_DIR"
docker inspect "$CID" > "$FAILED_BACKUP_DIR/container-inspect.json"
docker stop -t 30 "$CID"
test "$(docker inspect --format '{{.State.Running}}' "$CID")" = false
docker run --rm --user 0:0 --volumes-from "$CID:ro" \
  --entrypoint tar "$FAILED_IMAGE" -C /storage -cpf - . \
  > "$FAILED_BACKUP_DIR/storage.tar"
chmod 0600 "$FAILED_BACKUP_DIR/storage.tar"
test -s "$FAILED_BACKUP_DIR/storage.tar"
tar -tf "$FAILED_BACKUP_DIR/storage.tar" > "$FAILED_BACKUP_DIR/storage-contents.txt"
sha256sum "$FAILED_BACKUP_DIR/storage.tar" > "$FAILED_BACKUP_DIR/storage.tar.sha256"
```

If the failed container is already stopped, locate and inspect its recorded identity in Coolify/Docker and assign that stopped ID explicitly rather than expecting `docker ps` to find it. Its mount must remain available for this archive.

**Prefer an image-only rollback only when compatibility of the old code with the migrated schema/data has been verified.** Otherwise restore the cold backup and matching old image together. After live transactions, prefer a forward fix: restoring the earlier DB loses all writes since that backup, including account changes, uploads, orders, and payment/webhook state. Reconcile payment-provider events, webhooks, and affected orders before reopening traffic.

For a full restore, leave the failed storage untouched and extract to a **fresh sibling directory**, never merge over a running database or delete a volume. In the host shell, ensure `BACKUP_DIR` identifies the original verified pre-rollout archive:

```bash
sha256sum --check "$BACKUP_DIR/storage.tar.sha256"
RESTORE_DIR="/data/coolify/applications/odf-prod-restore-$(date -u +%Y%m%dT%H%M%SZ)"
sudo test ! -e "$RESTORE_DIR"
sudo mkdir -m 0755 "$RESTORE_DIR"
sudo tar --numeric-owner -xpf "$BACKUP_DIR/storage.tar" -C "$RESTORE_DIR"
sudo stat -c '%u:%g %a %n' "$RESTORE_DIR" "$RESTORE_DIR/data" "$RESTORE_DIR/data/odf_prod.db"
```

Use interactive sudo and preserve the archived numeric owners, especially `10001:10001` on `data` and its contents. Do not use the operator-owned `backup-check` extraction as the restore source. Do not chmod everything to `777`.

In the **same production Coolify app**:

1. Change the `/storage` bind source to the exact new `RESTORE_DIR`; retain `/storage` as destination and keep `DATABASE_PATH=/storage/data/odf_prod.db`.
2. Select the old pinned repository digest/image matching the recorded full image ID, not mutable `latest` or the observational digest in this document.
3. Restore the original Coolify variables/settings from the private pre-rollout snapshot, including the original unset/set behavior of `SERVICES_ENABLED` and `MEDIA_DIR`, and all original secrets. Do not carry new runtime defaults into the old release accidentally.
4. Confirm every old/failed container remains stopped and there is no competing writer. Deploy the old image with the restored bind.
5. Recapture the single running container, verify its image/mount/database, and repeat health and applicable public/admin checks. The old release may not have the new catalog UI. Reconcile lost writes before ending maintenance.

Retain both archives and the untouched failed storage securely until recovery and reconciliation are complete. Never remove storage simply to make Coolify deploy successfully.

## Runbook verification

The repository Dockerfile was built locally and exercised against disposable storage using the production-style `/storage/data/odf_prod.db` layout. Verification covered startup migrations and signage seeding with an existing administrator, non-root media directory creation, stopped-container archive/restore with preserved ownership and SQLite integrity, and the exact interactive credential script above. Create/reset succeeded with both password prompts hidden; mismatched confirmation was rejected; both resulting accounts authenticated to `/admin/catalog`. All Bash snippets passed syntax checking.

Registry publication, Coolify redeployment, production migration, production account setup, and full production rollback were **not executed**. Those remain operator steps in the runbook.
