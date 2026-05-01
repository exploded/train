# Train

iPhone-only mobile barbell workout tracker with auto-progression. Hosted at
[train.mchugh.au](https://train.mchugh.au).

## Stack

- Go (stdlib `net/http` + `html/template`)
- HTMX (vendored at `static/htmx.min.js`)
- sqlc + SQLite (`modernc.org/sqlite`, pure Go, `CGO_ENABLED=0`)
- Google OAuth (`coreos/go-oidc` + `golang.org/x/oauth2`)
- systemd on Linode Debian, deployed via GitHub Actions

## Local dev

1. `cp .env.example .env`
2. Set `SESSION_KEY` to 64 random hex chars (`openssl rand -hex 32`).
3. Set `DEV_USER_EMAIL=james67@gmail.com` to bypass Google OAuth.
4. `build.bat` (Windows) — builds and runs on http://localhost:8080.
5. Use Chrome DevTools iPhone emulation (Cmd+Opt+I → toggle device).

## DB

`db/schema.sql` is the source of truth (run on every startup with
`CREATE TABLE IF NOT EXISTS`). After editing `db/queries.sql`, run:

```
sqlc generate
```

The generated files (`db/db.go`, `db/models.go`, `db/queries.sql.go`) are
committed. Hand-written code (`db/migrate.go`, `db/seed.go`) lives in the
same package.

## Deploy

```
git push origin master
```

GitHub Actions builds a static Linux binary and SCPs it + assets to the
server, where `/usr/local/bin/deploy-train` (run via `sudo`) restarts the
systemd unit.

First-time server setup: see `scripts/server-setup.sh`.
