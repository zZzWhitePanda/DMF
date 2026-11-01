# Memeforge

Web dashboard + always-on backend for the Dank Memer grinder, deployed on Fly.io.

## layout

```
DMF/
├── backend/            Go server — grinder core + HTTP/WS API + Google OAuth
│   ├── cmd/server/     main entry
│   ├── internal/
│   │   ├── grinder/    the existing grinder (TUI-free)
│   │   ├── api/        HTTP + WebSocket handlers
│   │   ├── auth/       OAuth + sessions + middleware
│   │   ├── db/         SQLite layer (modernc.org/sqlite, pure Go)
│   │   └── runtime/    per-user grinder instance manager
│   ├── dashboard-build/  Next.js static output, embedded at build time
│   ├── Dockerfile
│   └── fly.toml
├── dashboard/          Next.js dashboard (TypeScript + Tailwind + shadcn/ui)
│   ├── app/            routes
│   ├── lib/            API client, HWID, utils
│   └── components/     UI primitives (populated as we grow)
└── admin/              (empty for now — key-gen site comes after dashboard works)
```

## local development

Prereqs: Go 1.26+, Node 22+, flyctl.

**1. Backend:**
```bash
cd backend
go mod tidy       # fetches dependencies
```

Create `.env` in `backend/` (gitignored). Replace `<YOUR_CLIENT_ID>` and
`<YOUR_CLIENT_SECRET>` with the real values from your Google Cloud
console OAuth credentials:

```bash
GOOGLE_CLIENT_ID=<YOUR_CLIENT_ID>
GOOGLE_CLIENT_SECRET=<YOUR_CLIENT_SECRET>
ADMIN_EMAIL=flynn.zipsin@gmail.com
PUBLIC_URL=http://localhost:8080
DATABASE_PATH=./memeforge.db
```

Run the server (loads .env via your shell — on Windows PowerShell:
`Get-Content .env | foreach { $k,$v = $_ -split "=",2; [Environment]::SetEnvironmentVariable($k,$v,"Process") }`):

```bash
go run ./cmd/server
```

Listens on `http://localhost:8080`.

**2. Dashboard (dev mode with hot reload):**
```bash
cd dashboard
npm install
npm run dev
```

Serves on `http://localhost:3000`. API calls proxy to the Go backend on :8080 via `next.config.js` rewrites.

Open `http://localhost:3000` → "Sign in with Google" → you log in → `license_status` returns `admin` because your email matches `ADMIN_EMAIL` → redirects to `/dashboard`.

## deploying to Fly.io

**One-time setup:**

```bash
# From the DMF repo root.
cd backend
flyctl launch --no-deploy
# When prompted:
#   - app name:       memeforge  (or whatever you want — must be globally unique)
#   - region:         syd        (or closest to you)
#   - Postgres:       No
#   - Redis:          No
#   - deploy now:     No
```

Create the persistent volume for SQLite:

```bash
flyctl volume create data --size 1 --region syd
```

Set your secrets. Substitute the real values (OAuth creds from Google
Cloud console, master secret from your password manager — ENI gave you
one in the setup conversation):

```bash
flyctl secrets set \
  GOOGLE_CLIENT_ID=<YOUR_CLIENT_ID> \
  GOOGLE_CLIENT_SECRET=<YOUR_CLIENT_SECRET> \
  MASTER_SECRET=<YOUR_MASTER_SECRET>
```

Update `PUBLIC_URL` in `fly.toml` to match the URL Fly assigns (default `https://memeforge.fly.dev`). Also add that URL to your Google OAuth client's **Authorized JavaScript origins** and **Authorized redirect URIs** (as `https://memeforge.fly.dev/auth/google/callback`).

**Deploy:**

```bash
cd .. && cd backend     # from repo root
flyctl deploy --dockerfile Dockerfile --build-arg GO_VERSION=1.26
```

First build takes 2-4 minutes (compiling Next.js + Go). Subsequent deploys are faster if the Dockerfile layers cache well.

Visit `https://memeforge.fly.dev` → sign in with Google → dashboard loads.

## redeploying after code changes

```bash
git add . && git commit -m "..." && git push
flyctl deploy
```

(Later we'll add a GitHub Action that auto-deploys on push to `main`.)

## stopping / restarting

```bash
flyctl machine list
flyctl machine stop <machine-id>
flyctl machine start <machine-id>
```

Or just redeploy — Fly handles the rolling restart.
