# vinted-notifier

A long-lived Go daemon that watches [Vinted](https://www.vinted.com/) searches
and posts **new listings** and **price changes** into a Matrix room. Searches are
managed at runtime with `!vinted` commands in that room.

## How it works

- Fetches Vinted's public catalog page (`/catalog?search_text=…&order=newest_first`,
  no login) for each search on an interval, and reads the listings out of the
  page's embedded Next.js data. Vinted's JSON API (`/api/v2/catalog/items`) is
  not reachable anonymously any more, so the page is the source.
- Stores every item it has seen per search in SQLite. On each poll:
  - an item not seen before → **new**;
  - a seen item whose price changed → **price change**;
  - otherwise → ignored.
- A newly added search is **seeded silently** on its first poll (all current
  items stored, no messages) so adding a search never floods the room.
- For each item it is about to notify about, it looks up the seller's country
  (see [Seller countries](#seller-countries)) and fetches the item page for the
  description and full photo gallery (catalog results carry only the main
  photo), so the extra requests scale with events, not with listings.
- Runs a Matrix client (mautrix-go) in the same process for commands + output.

Each notification sends the item's **main photo** with a caption (title, price,
brand, condition, seller location, truncated description) and a link. Any **additional photos**
are posted as replies **in a thread** under that message. Items with no photo
fall back to a text message. Images are re-uploaded to your homeserver's media
repo so they render inline in any client. Vinted serves photos as WebP.

## Choosing the Vinted site

Set `vinted.domain` to the Vinted site you want, e.g. `www.vinted.pt`,
`www.vinted.fr`, `www.vinted.es`, `www.vinted.de`, `www.vinted.co.uk`. The site
decides:

- the language of condition labels and category names (`Muito bom` vs `Très bon état`);
- the currency and the links posted to the room;
- the ranking of results.

The European sites share one catalog, so the same listing usually appears on
all of them with the same id. Category ids are shared too, though names are
translated.

## Seller countries

Because the catalog is shared, a search on vinted.pt also returns items from
sellers in Italy, France, Spain and so on, and Vinted's search has no
seller-country filter. The notifier therefore filters on its side: before
notifying about an item it reads the seller's country from their public profile
(`/member/<id>`) and skips items from sellers outside `vinted.countries`.

```yaml
vinted:
  domain: "www.vinted.pt"
  countries: [PT, ES]   # omit = the domain's country only (PT here); [ALL] = no filter
```

- Seller locations are cached in the database for 30 days, so each seller costs
  at most one extra request; only items about to be notified are looked up.
- If a lookup fails, the item is notified anyway rather than silently lost.
- The seller's city, when they choose to show it, is included in the caption.
- Skipped items still count as seen (they show up in `!vinted list` counts) and
  are logged as `skipped N item(s) from sellers outside [PT]`.

## Build

```sh
go build -o vinted-notifier .
```

## Configure

Copy `config.example.yaml` to `config.yaml` and fill in your Matrix homeserver,
user id, access token, room id and Vinted site:

```yaml
matrix:
  homeserver: "https://matrix.org"
  user_id: "@vintedbot:matrix.org"
  access_token: "syt_..."
  room_id: "!abc:matrix.org"
vinted:
  domain: "www.vinted.pt"   # required; any Vinted site
  # countries: [PT]         # seller countries to notify about; default = the domain's country
poll:
  interval_seconds: 300     # minimum 30
  max_pages: 2              # pages per search per poll (96 newest items/page)
  jitter_percent: 10        # spread each tick by ±this much (0-50, 0 disables)
db_path: "./vinted.db"
```

`config.yaml` and `*.db` are gitignored.

Each poll waits `interval_seconds` ± `jitter_percent`, so the daemon does not
hit Vinted on a perfectly regular beat. All requests go through one pacer
(≥1.5 s apart) shared by polling and chat commands. A catalog page is about
0.5 MB compressed, so each search costs roughly `max_pages × 0.5 MB` per poll.

If every search fails in a cycle — an outage, or Vinted's bot protection
blocking this host — the bot posts a warning to the room and posts again once
polling recovers, so the silence is never mistaken for "no new items". If
Vinted changes its page layout, polls fail with `unexpected Vinted page layout`
in the log and the same warning in the room.

## Run

```sh
./vinted-notifier -config config.yaml
```

## Docker

Each push builds a multi-arch image (`linux/amd64`, `linux/arm64`) and publishes
it to GHCR as `ghcr.io/ricardo-duarte-av/vinted-notifier`, tagged with the branch
name, the commit sha, and `latest` on the default branch.

The container keeps all state in `/data`, which is meant to be a bind mount from
the host: you put `config.yaml` there, and the daemon writes `vinted.db` next to
it. Set `db_path` accordingly:

```yaml
db_path: "/data/vinted.db"
```

```sh
mkdir -p data
cp config.example.yaml data/config.yaml   # then fill it in, set db_path: /data/vinted.db
docker run -d --name vinted-notifier --restart unless-stopped \
  -v "$PWD/data:/data" ghcr.io/ricardo-duarte-av/vinted-notifier:latest
```

Or with the bundled `docker-compose.yaml`:

```sh
docker compose up -d
docker compose logs -f
```

The image runs as uid `10001`, so the host directory must be writable by it
(`chown -R 10001:10001 data`) — otherwise uncomment `user:` in the compose file
and run as your own uid. `data/` is gitignored.

## Matrix commands

Send these in the configured room:

| Command | Description |
| --- | --- |
| `!vinted add "<query>" <min> <max> <catalog_id>` | Add a search. Use `-` to skip a filter. Prices are whole currency units. |
| `!vinted categories [term\|id]` | No arg lists the top-level sections; a number drills into that category's children; text searches by name (in the site's language). Use the id as `<catalog_id>`. |
| `!vinted list` | List searches with their `#index`, state, filters and item counts. |
| `!vinted disable <index>` | Stop searching an entry (kept in the DB). |
| `!vinted enable <index>` | Resume a disabled entry (silently re-baselines on next poll). |
| `!vinted delete <index>` | Permanently delete an entry and its stored results. |
| `!vinted` / `!vinted help` | Show the command list. |

The `<index>` is the `#N` shown by `!vinted list`. Disabling keeps the entry and
its stored items but skips it while polling; enabling resets its baseline so
items posted while it was disabled are absorbed silently rather than firing a
burst of notifications.

Example — iPhones between 100 and 300 € in the mobile phones catalog (id 3661):

```
!vinted add "iphone" 100 300 3661
```

The four filters map to the catalog URL parameters `search_text`, `price_from`,
`price_to` and `catalog[]`.

### Multi-user

Each search records the Matrix user who added it. By default users only see and
manage their own searches:

- `!vinted list` shows only your searches; `disable`/`enable`/`delete` only work
  on searches you own.
- **Room moderators** (power level ≥ 50) see everyone's searches in
  `!vinted list` (with the owner shown) and can `disable`/`enable`/`delete` any
  of them.

When a new listing or price change is found, the notification **pings the
search owner** (via an `m.mentions` mention at the end of the caption).

## Tests

```sh
go test -short ./...                            # offline unit tests (parser fixtures in internal/vinted/testdata)
go test ./internal/vinted -run TestLive -v      # hits live Vinted (www.vinted.fr by default)
VINTED_DOMAIN=www.vinted.pt go test ./internal/poller -run TestLive -v
```
