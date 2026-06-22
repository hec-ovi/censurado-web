# Censurado deploy (Phase 6B)

Self-hosting topology for the three Go binaries. Single-writer sqlite on one shared
volume in WAL mode:

- publish: the only db writer. Authenticated write API (POST /articles, GET /healthz).
- admin: a db reader. Private operator console (/admin/*); writes the site only on regenerate.
- generate: a db reader. One-shot static site builder, run on demand.

publish and admin are private: their host ports bind to 127.0.0.1 only (reach them
over an SSH tunnel or private network). The only public surface is the generated
static site on the site-data volume, served by an external CDN or web server that is
not part of this compose.

## Setup

Everything below runs from the `deploy/` directory.

1. Create your env file and fill in real values:

   ```
   cp .env.example .env
   ```

2. Generate the admin credentials (prints token, hash, and session key once):

   ```
   docker compose run --rm admin -gen-credentials
   ```

   Put `CENSURADO_ADMIN_TOKEN_HASH` and `CENSURADO_ADMIN_SESSION_KEY` in `.env`.
   Keep the `CENSURADO_ADMIN_TOKEN` for the operator; do not store it on the server.

3. Create the publish keys file, then mint at least one key:

   ```
   printf '[]\n' > keys.json
   docker compose run --rm publish -gen-key -author ada -scope articles:write
   ```

   Seed `keys.json` as an empty JSON array FIRST. The publish service bind-mounts
   `keys.json`, so if the file is missing Docker creates an empty directory in its
   place and this first `compose run` breaks. (`-gen-key` itself never reads the
   file; it mints and exits.) Then replace the `[]` with the printed JSON entry
   (the file is a JSON array of entries), and hand the printed `TOKEN` to the agent
   as `CENSURADO_PUBLISH_TOKEN`.

## Run

Bring up the private services (publish + admin):

```
docker compose up -d
```

publish is on 127.0.0.1:8080, admin on 127.0.0.1:8081.

Run a static site build on demand (writes the site-data volume, then exits):

```
docker compose run --rm generate
```

## Notes

- The db-data volume is mounted read-write for all three services. WAL readers need
  to touch the -wal/-shm sidecars, so a read-only mount would break them. Only publish
  writes article data.
- Images are distroless, non-root, and cgo-free static builds (modernc.org/sqlite is
  pure Go).
- The litestream backup sidecar comes in the next phase (6C). The compose file has a
  commented extension point for it.
