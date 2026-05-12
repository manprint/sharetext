# sharetext

Webapp per condividere snippet di testo (e file) in tempo reale.
Backend Go single-binary, persistenza SQLite, sync via WebSocket, frontend vanilla JS.

## Indice

- [Caratteristiche](#caratteristiche)
- [Avvio rapido](#avvio-rapido)
- [Variabili d'ambiente](#variabili-dambiente)
- [API sessioni](#api-sessioni)
- [Allegati (file)](#allegati-file)
- [Formato blocchi](#formato-blocchi)
- [Vista righe & UI mobile](#vista-righe--ui-mobile)
- [Admin](#admin)
- [Sessioni temporanee — scadenza](#sessioni-temporanee--scadenza)
- [Pulizia DB](#pulizia-db)
- [Versioning](#versioning)
- [Test](#test)
- [Docker](#docker)
- [Struttura](#struttura)

---

## Caratteristiche

**Sessioni**

- Due modalità a scelta nella landing page:
  - **Persistente** — nome obbligatorio (regex `^[A-Za-z0-9_-]{1,32}$`), preposto allo slug random (forma finale `{nome}-{rand}`). Resta finché non viene rimossa dall'admin.
  - **Temporanea** — durata obbligatoria in minuti (1..10080 = 7 giorni). Allo scadere viene hard-deleted, irrecuperabile.
- Slug crypto-random base58-ish (alfabeto senza caratteri ambigui).
- Chiunque conosce il link legge e scrive.
- Persistenza su SQLite (modalità WAL, FK abilitate).

**Sync realtime**

- WebSocket per ogni stanza (`/ws/{slug}`).
- Hub server-side: broadcast a tutti i peer della stessa sessione su ogni update.
- Stato iniziale inviato sulla connessione.
- Last-write-wins.

**Editor**

- Textarea + pannello "righe" affiancato (desktop) o impilato (mobile, single column).
- **Copia/Scarica per riga**, **Copia/Scarica per blocco multi-riga**, **Copia/Scarica tutto** in toolbar.
- Sintassi blocchi `-----` per raggruppare più righe in un'unica voce.
- Countdown live nell'header per sessioni temporanee; overlay "Sessione scaduta" allo zero.

**Allegati**

- Upload via bottone (selettore file, accoda alla fine del testo) o **drag & drop** sull'editor (inserisce nella riga dove avviene il drop).
- Marker testuale `[file:<id>:<url-encoded-name>]` reso come riga speciale nel pannello righe (icona 📎, nome, size, download).
- Download per singolo file dalla riga, oppure **bundle ZIP** (testo + tutti gli allegati) tramite il pulsante Scarica di sessione.
- Backend storage configurabile: **SQLite BLOB** (`db`, comportamento legacy) oppure **filesystem** (`fs`).
- Limiti operativi configurabili: `MAX_FILE_SIZE`, `MAX_FILES_PER_SESSION`, `MAX_SESSION_STORAGE_BYTES`.
- File cascade-deleted con la sessione; orfani (marker rimossi dal testo) ripuliti dal cleanup periodico con grace window e riferimenti file normalizzati nel DB.

**Admin**

- Pannello `/admin` protetto da HTTP Basic Auth (confronto costante via `crypto/subtle`).
- Elenco sessioni attive con metadata: tipo, dimensione (testo + somma allegati), creata/aggiornata/scade.
- Eliminazione singola sessione (hard delete, FK cascade rimuove anche i file).
- Audit log persistente delle delete admin.
- Endpoint dedicato per metriche operative e stato dell'hub WebSocket.

**Mobile**

- Layout responsivo, breakpoint ≤800px (single column) e ≤640px (zoom 90%, padding ridotti, hit-target più ampi, no auto-zoom su focus input/textarea).
- Pulsante toggle "Modifica" ↔ "Righe": di default su mobile vede solo la lista, l'editor compare dopo il toggle.
- Tabella admin mobile → cards verticali via `data-label`.
- `caretPositionFromPoint` + fallback `caretRangeFromPoint` per drag-drop.

**Versioning**

- `internal/version.Version` (default `v1.0.0`), mostrato accanto al nome app in tutte le pagine e nei log di boot.
- Override a build-time via `-ldflags` o `--build-arg VERSION=` (pronto per GitHub Actions tag-driven).

**Pulizia**

- Goroutine ogni `CLEANUP_INTERVAL`: hard-delete sessioni scadute + sweep file orfani (con grace).
- DB sempre normalizzato; safety net SQL anche se le FK fossero off.
- I riferimenti `[file:...]` vengono mantenuti incrementalmente in una tabella derivata, evitando full scan del testo a ogni cleanup.

**Docker**

- Image distroless `static:nonroot`, binary statico, volume `/data` con perms preallocate per UID 65532.
- Compose pronto, supporto build-arg per versione.

---

## Avvio rapido

```bash
# Run locale
go run ./cmd/server
# oppure
just run

# Run via Docker compose
just up        # build + start
just smoke     # health checks contro :8080
just down
```

Apri `http://localhost:8080`. Crea una sessione **Persistente** (richiede nome) o **Temporanea** (richiede minuti). Il browser viene reindirizzato su `/s/{slug}`.

---

## Variabili d'ambiente

| Variabile | Default | Descrizione |
|-----------|---------|-------------|
| `PORT` | `8080` | Porta HTTP |
| `DB_PATH` | `sharetext.db` | File SQLite |
| `SLUG_LEN` | `16` | Lunghezza della parte random dello slug |
| `CLEANUP_INTERVAL` | `30s` | Frequenza sweep cancellazione sessioni scadute + allegati orfani |
| `REQUEST_TIMEOUT` | `30s` | Timeout middleware per le richieste HTTP |
| `VACUUM_INTERVAL` | `0s` (off) | Frequenza `VACUUM` SQLite + `wal_checkpoint(TRUNCATE)` per reclaim spazio. `0` o vuoto = disabilitato. |
| `FILE_GRACE` | `60s` | Finestra di grazia per upload appena fatti (evita race su marker) |
| `MAX_FILE_SIZE` | `10485760` | Limite massimo upload per singolo file in byte. Default 10 MiB. |
| `MAX_CONTENT_SIZE` | `4194304` | Limite massimo del contenuto testuale di sessione in byte. Vale per `PUT` e WebSocket. |
| `MAX_FILES_PER_SESSION` | `256` | Numero massimo di allegati per sessione. `0` disabilita il limite. |
| `MAX_SESSION_STORAGE_BYTES` | `104857600` | Quota massima complessiva per sessione: `len(content) + somma(size files)`. `0` disabilita il limite. |
| `FILE_STORAGE_BACKEND` | `db` | Backend allegati: `db` (BLOB in SQLite) oppure `fs` (filesystem). |
| `FILE_STORAGE_DIR` | `dirname(DB_PATH)/sharetext-files` | Directory base usata quando il backend allegati e' `fs`. |
| `RATE_LIMIT_ENABLED` | `true` | Abilita il rate limit per-IP sui route pubblici e admin. |
| `RATE_LIMIT_RPS` | `20` | Token/sec del rate limit pubblico. |
| `RATE_LIMIT_BURST` | `60` | Burst massimo del rate limit pubblico. |
| `RATE_LIMIT_TTL` | `10m` | TTL degli entry IP del rate limiter pubblico. |
| `ADMIN_RATE_LIMIT_RPS` | `5` | Token/sec del rate limit admin. |
| `ADMIN_RATE_LIMIT_BURST` | `15` | Burst massimo del rate limit admin. |
| `ADMIN_RATE_LIMIT_TTL` | `10m` | TTL degli entry IP del rate limiter admin. |
| `READ_HEADER_TIMEOUT` | `5s` | Timeout di lettura degli header HTTP. |
| `WRITE_TIMEOUT` | `30s` | Timeout di scrittura della risposta HTTP. |
| `IDLE_TIMEOUT` | `2m` | Timeout keep-alive HTTP. |
| `MAX_HEADER_BYTES` | `1048576` | Limite massimo per gli header HTTP. |
| `SECURITY_HEADERS_ENABLED` | `true` | Abilita CSP e altri header di hardening HTTP. |
| `CONTENT_SECURITY_POLICY` | vedi default | Valore completo dell'header `Content-Security-Policy`. |
| `FRAME_OPTIONS` | `DENY` | Valore di `X-Frame-Options`. |
| `REFERRER_POLICY` | `no-referrer` | Valore di `Referrer-Policy`. |
| `PERMISSIONS_POLICY` | `camera=(), microphone=(), geolocation=()` | Valore di `Permissions-Policy`. |
| `STRICT_TRANSPORT_SECURITY` | _(unset)_ | Se impostata, viene inviata come `Strict-Transport-Security` su richieste HTTPS/proxy HTTPS. |
| `METRICS_ENABLED` | `true` | Abilita la raccolta di metriche operative in memoria e l'endpoint admin dedicato. |
| `AUDIT_LOG_ENABLED` | `true` | Abilita la persistenza degli audit log admin. |
| `AUDIT_LOG_DEFAULT_LIMIT` | `50` | Limite di default di `/admin/api/audit` quando manca `?limit=`. |
| `ADMIN_USER` | _(unset)_ | Username Basic Auth per `/admin`. Se vuoto, admin disabilitato (503). |
| `ADMIN_PASS` | _(unset)_ | Password Basic Auth per `/admin`. Se vuota, admin disabilitato (503). |

Compose passa tutto via env override (`${VAR:-default}`).

Nota: il file `compose.yaml` incluso override alcuni default applicativi. In particolare imposta `VACUUM_INTERVAL=5m` e, se non sovrascritti, `ADMIN_USER=admin` e `ADMIN_PASS=changeme`.

---

## API sessioni

### `POST /api/sessions`

Crea una sessione. Body JSON obbligatorio:

```jsonc
// Persistente
{ "type": "persistent", "name": "team-alpha" }

// Temporanea (60 minuti)
{ "type": "temporary",  "minutes": 60 }
```

Validazione:

- `type` ∈ `{persistent, temporary}`.
- `name`: 1–32 caratteri, solo `[A-Za-z0-9_-]`. Obbligatorio per persistent.
- `minutes`: intero in `[1, 10080]`. Obbligatorio per temporary.

Risposta `201 Created`:

```jsonc
{
  "slug": "team-alpha-3vRdM58dftguriSe",
  "url":  "/s/team-alpha-3vRdM58dftguriSe",
  "name": "team-alpha",            // omesso se temporary
  "expires_at": null               // ISO-8601 UTC se temporary
}
```

Errori: `400 Bad Request` su validazione, `500` su errore interno.

### `GET /api/sessions/{slug}`

```jsonc
{
  "slug": "...",
  "name": "team-alpha",                  // omesso per le temporanee
  "content": "...",
  "updated_at": "2026-05-12T14:00:00Z",
  "expires_at": "2026-05-12T15:00:00Z"   // omesso per le persistenti
}
```

Codici: `200` ok, `404` sconosciuto, `410 Gone` quando la sessione è scaduta (anche prima dello sweep).

### `PUT /api/sessions/{slug}`

Body `{"content": "..."}`. Stessi codici (`200/400/404/410`) piu' `413` se `content` supera `MAX_CONTENT_SIZE` oppure la quota totale della sessione (`MAX_SESSION_STORAGE_BYTES`). Sul successo il server fa broadcast su tutti i WebSocket attivi della stessa stanza.

### `GET /ws/{slug}` — WebSocket

Messaggi JSON `{"content": "..."}` in entrambe le direzioni. Stato iniziale inviato alla connessione. Connessione rifiutata con `404` se lo slug non esiste o è scaduto. Messaggi oltre `MAX_CONTENT_SIZE` chiudono la connessione con close status `1009` (`message too big`).

### Esempi

```bash
# Persistente
curl -X POST -H 'content-type: application/json' \
  -d '{"type":"persistent","name":"team-alpha"}' \
  http://localhost:8080/api/sessions

# Temporanea (5 min)
curl -X POST -H 'content-type: application/json' \
  -d '{"type":"temporary","minutes":5}' \
  http://localhost:8080/api/sessions

# Update contenuto
curl -X PUT -H 'content-type: application/json' \
  -d '{"content":"hello"}' \
  http://localhost:8080/api/sessions/<slug>
```

---

## Allegati (file)

Una sessione può ospitare allegati binari. Il metadata vive sempre nel DB con FK `ON DELETE CASCADE` sullo slug, quindi sparisce insieme alla sessione (admin delete o scadenza temporanea). Il payload del file puo' stare:

- in SQLite (`FILE_STORAGE_BACKEND=db`, comportamento legacy);
- su filesystem (`FILE_STORAGE_BACKEND=fs`, file sotto `FILE_STORAGE_DIR/<slug>/<id>`).

Il backend viene registrato per ogni upload: cambiare backend in seguito non rompe gli allegati esistenti.

### Marker testuale

Ogni allegato è rappresentato nell'editor da una **riga marker**:

```
[file:<id>:<url-encoded-name>]
```

- `id` — alfanumerico (12 char) generato dal server.
- `name` — filename originale, URL-encoded (`encodeURIComponent`) per tollerare spazi, `:`, `]`, accenti.

La riga deve essere intera (spazi prima/dopo tollerati). I marker possono convivere con righe normali e blocchi `-----`.

### Caricamento

- **Bottone Upload** (header): apre selettore file, accoda i marker dopo l'ultima riga (con `\n` separatore).
- **Drag & drop sull'editor**: i marker vengono inseriti come righe nuove all'inizio della riga in cui è avvenuto il drop (caret risolto via `caretPositionFromPoint` + fallback `caretRangeFromPoint`, ultimo fallback fine testo).
- Persistenza: dopo l'upload il client fa `PUT /api/sessions/{slug}` esplicita (più affidabile del solo WS, soprattutto su mobile dove la connessione può non essere ancora aperta), e il server fa broadcast a tutti i peer.
- Limite per file: `MAX_FILE_SIZE` (default 10 MiB) → eccesso → `413 Request Entity Too Large`.
- Limiti di quota: `MAX_FILES_PER_SESSION` e `MAX_SESSION_STORAGE_BYTES`. Se superati, il server risponde `413`.
- Validazione anticipata: upload su slug inesistente o scaduto viene rifiutato prima del parsing completo del multipart.

### Download

- **Sezione righe**: ogni marker rende come riga `.file` con icona 📎, nome, dimensione (da `/files`), pulsante **Scarica** → GET diretto su `/api/sessions/{slug}/files/{id}`.
- **Bottone "Scarica" di sessione**: zip server-side `{slug}.zip` con:
  - `{slug}.txt` (contenuto raw dell'editor, marker compresi).
  - `files/<filename>` per ogni allegato (nomi duplicati → `name-2.ext`, `name-3.ext`, ...).

### Endpoint REST file

| Metodo  | Path                                            | Effetto                                  |
|---------|-------------------------------------------------|------------------------------------------|
| POST    | `/api/sessions/{slug}/files`                    | Upload multipart (`file` field)          |
| GET     | `/api/sessions/{slug}/files`                    | Lista metadata (`{files:[…], count}`)    |
| GET     | `/api/sessions/{slug}/files/{id}`               | Download binario con `Content-Disposition: attachment; filename="..."; filename*=UTF-8''...` |
| GET     | `/api/sessions/{slug}/bundle`                   | ZIP testo + allegati                      |

```bash
# upload
curl -F 'file=@/path/to/notes.pdf' http://localhost:8080/api/sessions/<slug>/files

# download singolo
curl -OJ http://localhost:8080/api/sessions/<slug>/files/<id>

# bundle ZIP
curl -OJ http://localhost:8080/api/sessions/<slug>/bundle
```

---

## Formato blocchi

Un gruppo di righe può essere marcato come **blocco** racchiudendolo fra due righe contenenti esattamente `-----` (ammessi spazi a inizio/fine). Nel pannello righe il blocco viene mostrato come **un'unica voce** con bottoni `Copia blocco` / `Scarica blocco` che agiscono sul contenuto interno (delimitatori esclusi).

```
prima riga
-----
services:
  sharetext:
    build: .
-----
ultima riga
```

Render:

| # | Tipo  | Contenuto                                 |
|---|-------|-------------------------------------------|
| 1 | riga  | `prima riga`                              |
| 2 | blocco| `services:\n  sharetext:\n    build: .`   |
| 3 | riga  | `ultima riga`                             |

Regole:

- Delimitatori `-----` non inclusi nel contenuto copiato/scaricato del blocco.
- Delimitatore "spaiato" (numero dispari di `-----`) resta una riga normale.
- Due `-----` consecutivi formano un blocco vuoto.
- Una riga che *contiene* `-----` ma non è composta solo da `-----` (con eventuali spazi) NON è un delimitatore.
- Il pulsante **Copia tutto** copia il contenuto raw dell'editor, delimitatori compresi (preserva round-trip).
- **Righe vuote**: niente bottoni Copia/Scarica.
- **Marker file** dentro un blocco: l'intero blocco resta `block`, il marker non viene riconosciuto come riga file (coerente con la natura "raw" dei blocchi).

---

## Vista righe & UI mobile

**Desktop** — layout a due colonne: editor a sinistra, pannello righe a destra. Pulsanti Copia/Scarica delle voci visibili in hover.

**Mobile** (≤800px):

- Layout single column.
- Vista **righe** mostrata di default; nessun editor finché l'utente preme il pulsante header `Modifica`. Re-click → `Righe`. Lo switch è puramente CSS (`.session.editing`) + classe aggiunta via JS.
- Tutti i pulsanti Copia/Scarica sempre visibili (`@media (hover: none)`).
- `input[type=file]` posizionato off-screen (`.sr-only`) anziché `display:none` → `.click()` programmatica funziona su iOS Safari.

**Mobile compact** (≤640px):

- `body { zoom: 0.9 }` per scaling uniforme.
- Font input/textarea forzato a 16px → niente auto-zoom iOS al focus.
- Tabella admin → cards verticali via `data-label` su ogni `<td>` (`thead` nascosto).
- Hit-target ≥38px sui bottoni header, ≥42px sul pulsante Elimina admin.

Viewport meta: `width=device-width, initial-scale=1, viewport-fit=cover`. `theme-color: #0f766e` per UI di sistema (status bar mobile, browser chrome).

---

## Admin

Pannello `/admin` protetto da HTTP Basic Auth. Mostra tutte le sessioni non scadute con metadata completi e pulsante **Elimina** (hard delete + FK cascade su files).

### Abilitazione

Setta entrambe le variabili `ADMIN_USER` e `ADMIN_PASS`. Se una è vuota, qualsiasi richiesta sotto `/admin` risponde `503 Service Unavailable`.

```bash
ADMIN_USER=admin ADMIN_PASS=secret just run
# via compose: override in compose.yaml o tramite .env nella stessa cartella
```

Attenzione: il `compose.yaml` del repository abilita l'admin di default con `admin/changeme` se non sovrascrivi esplicitamente le env. In ambienti reali conviene cambiarle o svuotarle entrambe per disabilitare il pannello.

### Endpoints

| Metodo  | Path                              | Effetto                                  |
|---------|-----------------------------------|------------------------------------------|
| GET     | `/admin`                          | HTML del pannello                        |
| GET     | `/admin/api/sessions`             | JSON elenco sessioni attive              |
| GET     | `/admin/api/audit`                | JSON audit log admin                     |
| GET     | `/admin/api/metrics`              | JSON metriche operative + stato hub      |
| DELETE  | `/admin/api/sessions/{slug}`      | Elimina sessione (hard delete + cascade) |

Esempio JSON `/admin/api/sessions`:

```jsonc
{
  "count": 2,
  "sessions": [
    {
      "slug": "team-alpha-3vRdM58dftguriSe",
      "name": "team-alpha",
      "type": "persistent",
      "content_size": 421,
      "files_size": 1024,
      "files_count": 2,
      "total_size": 1445,
      "created_at": "2026-05-12T14:00:00Z",
      "updated_at": "2026-05-12T14:05:32Z"
    },
    {
      "slug": "p3zXjUUcvc9SPRdX",
      "type": "temporary",
      "content_size": 0,
      "files_size": 0,
      "files_count": 0,
      "total_size": 0,
      "created_at": "2026-05-12T14:09:21Z",
      "updated_at": "2026-05-12T14:09:21Z",
      "expires_at": "2026-05-12T14:39:21Z"
    }
  ]
}
```

- `content_size`: byte del testo.
- `files_size`: somma dei byte di tutti gli allegati della sessione.
- `files_count`: numero di allegati.
- `total_size`: `content_size + files_size`.

La UI mostra solo `total_size` nella colonna Size.

```bash
curl -u admin:secret http://localhost:8080/admin/api/sessions
curl -u admin:secret http://localhost:8080/admin/api/audit?limit=20
curl -u admin:secret http://localhost:8080/admin/api/metrics
curl -u admin:secret -X DELETE http://localhost:8080/admin/api/sessions/<slug>
```

Esempio JSON `/admin/api/audit`:

```jsonc
{
  "enabled": true,
  "count": 1,
  "entries": [
    {
      "id": 12,
      "actor": "admin",
      "action": "admin.delete_session",
      "target": "team-alpha-3vRdM58dftguriSe",
      "created_at": "2026-05-12T15:01:00Z"
    }
  ]
}
```

Esempio JSON `/admin/api/metrics`:

```jsonc
{
  "enabled": true,
  "active_rooms": 2,
  "active_connections": 5,
  "metrics": {
    "enabled": true,
    "sessions_created": 14,
    "session_updates": 93,
    "files_uploaded": 7,
    "file_downloads": 11,
    "bundles_generated": 3,
    "cleanup_runs": 10,
    "cleanup_deleted_sessions": 2,
    "cleanup_deleted_files": 4,
    "vacuum_runs": 1
  }
}
```

Note di sicurezza:

- Credenziali confrontate in tempo costante (`crypto/subtle`).
- Basic Auth viaggia in chiaro: dietro reverse proxy usare sempre HTTPS.
- I route admin hanno rate limit IP separato rispetto ai route pubblici.
- Le sessioni scadute non compaiono in lista (filtrate via SQL `expires_at IS NULL OR expires_at > now`).

---

## Sessioni temporanee — scadenza

- `expires_at` viene calcolato server-side al `POST /api/sessions` come `now + minutes*60s` (UTC).
- Ogni `GET`/`PUT`/`WS` controlla l'`expires_at` correntemente in DB: una sessione scaduta restituisce `410 Gone` anche prima che il job di cleanup la rimuova.
- Una goroutine eseguita ogni `CLEANUP_INTERVAL` lancia `DELETE FROM sessions WHERE expires_at <= now()` — **hard delete, non recuperabile**.
- In UI:
  - countdown live nell'header con formato `MM:SS` (o `HH:MM:SS` oltre l'ora);
  - colore arancione nell'ultimo minuto, rosso allo zero;
  - allo zero appare un overlay "Sessione scaduta", editor disabilitato, WebSocket chiuso.

---

## Vacuum DB (reclaim spazio)

SQLite in modalità WAL non rilascia automaticamente lo spazio su filesystem dopo le `DELETE` (sessioni scadute, allegati orfani, delete da admin): le pagine restano "free" dentro il file `.db` e il file `.db-wal` cresce con le modifiche. Per ridurre realmente l'occupazione su disco serve `VACUUM` + `PRAGMA wal_checkpoint(TRUNCATE)`.

L'app espone una goroutine periodica controllata dalla variabile `VACUUM_INTERVAL`:

- `0s` (default) o variabile non settata → job **disabilitato**, comportamento legacy invariato.
- Valore > 0 (es. `1h`, `24h`) → ogni tick esegue, in ordine:
  1. `PRAGMA wal_checkpoint(TRUNCATE)` — flush e troncamento del WAL pre-vacuum.
  2. `VACUUM` — riscrive il file principale rimuovendo le pagine libere.
  3. `PRAGMA wal_checkpoint(TRUNCATE)` — tronca il WAL prodotto dal VACUUM stesso.

Log per esecuzione (a info level):

```
vacuum: ok in 142ms (db=41943040B→1245184B, wal=40894464B→0B)
```

Avvertenze:

- `VACUUM` riscrive l'intero DB e tiene un lock di scrittura per la durata: i `PUT /api/sessions/{slug}` vengono bloccati finché finisce. Su DB piccoli (≤100 MiB) è sub-secondo; su DB più grandi pianifica l'intervallo di conseguenza.
- `VACUUM` richiede spazio temporaneo pari (in worst case) alla dimensione del DB.
- Il job non gira al boot: il primo run avviene dopo `VACUUM_INTERVAL` per evitare di ritardare l'apertura del listener.
- Errori del `wal_checkpoint` (es. WAL busy) sono **non fatali** e silenziosi; solo l'errore di `VACUUM` viene loggato.

Esempio compose:

```yaml
environment:
  VACUUM_INTERVAL: "24h"
```

## Pulizia DB

La goroutine `runCleanup` esegue a ogni tick (`CLEANUP_INTERVAL`):

1. **Sessioni scadute** — `DELETE FROM sessions WHERE expires_at <= now()`. I file collegati vengono cascade-deleted via FK.
2. **`DeleteOrphanFiles(grace)`**:
  - **Safety net**: rimuove righe `files` con `session_slug` non più presente in `sessions` (eseguito anche se FK fossero disabilitate, per resilienza in caso di DB incoerente).
  - **Per-sessione**: usa la tabella derivata `file_refs`, sincronizzata a ogni `PUT`, per eliminare i file della sessione che non risultano piu' referenziati *e* il cui `created_at <= now() - FILE_GRACE`.
  - **Filesystem**: se `FILE_STORAGE_BACKEND=fs`, rimuove anche i payload orfani e le directory sessione rimaste vuote.

`FILE_GRACE` (default 60s) protegge gli upload appena fatti il cui marker non è ancora stato propagato via WS/PUT, evitando di cancellare un file che sta per essere referenziato.

Garanzie complessive:

- Sessione persistente eliminata via admin → file cascade-deleted.
- Sessione temporanea scaduta → sweep DELETE + cascade.
- Marker rimosso dal testo (utente cancella la riga) → dopo `FILE_GRACE` dal momento dell'upload, prossimo tick elimina il file.
- Upload appena fatto con marker non ancora committed → protetto da grace.

---

## Versioning

`internal/version.Version` (default `v1.0.0`) è esposta accanto al nome dell'app in tutte le pagine (`index`, `session`, `admin`) e nei log di boot.

Override al build:

```bash
# locale via just
VERSION=v1.2.3 just build

# go nudo
go build -ldflags "-X sharetext/internal/version.Version=v1.2.3" ./cmd/server

# docker
docker build --build-arg VERSION=v1.2.3 -t sharetext .

# compose
VERSION=v1.2.3 docker compose build
```

### GitHub Actions

Workflow di release Docker che inietta il tag come versione:

```yaml
name: release
on:
  push:
    tags: ["v*"]
jobs:
  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          build-args: |
            VERSION=${{ github.ref_name }}
          tags: |
            ghcr.io/${{ github.repository }}:${{ github.ref_name }}
            ghcr.io/${{ github.repository }}:latest
```

Build non-Docker (release binary):

```yaml
- run: go build -ldflags "-s -w -X sharetext/internal/version.Version=${{ github.ref_name }}" -o sharetext ./cmd/server
```

---

## Test

```bash
just test-all       # Go + JS
just test           # solo Go
just test-race      # Go con -race
just test-js        # solo JS (node:test)
```

### Go

- `internal/session`:
  - `NewSlug` (length, alfabeto, unicità su 1000 iterazioni);
  - `ValidName` (regex, edge case lunghezza, accenti, caratteri proibiti);
  - `Compose` (anonymous, named, validazione).
- `internal/store`:
  - CRUD `sessions`: create + get + update + duplicati + concorrenza + Exists + Delete;
  - scadenza: `Get`/`Update`/`Exists` restituiscono `ErrExpired`; `DeleteExpired` (mix persistent/temporary/future/past);
  - `ListActive` (esclusione scadute, ordering, aggregazione `files_size`/`files_count`);
  - `files`: AddFile (missing/expired session), GetFile, ListFiles, DeleteFile;
  - cascade FK su session delete e DeleteExpired;
  - `ReferencedFileIDs` (regex marker, edge case);
  - `DeleteOrphanFiles`: unreferenced + grace protection + safety-net fallback con FK off.
- `internal/handlers`:
  - API sessioni (httptest): persistent/temporary validi e invalidi (400), 410 su scaduta su GET e PUT;
  - hub broadcast / except / leave;
  - WebSocket end-to-end con 2 client + slug inesistente;
  - admin: missing/wrong creds (401), creds vuote (503), list (200 con esclusione scaduti, total_size con files), delete (200 + 404 idempotenza), delete unauthorized;
  - file: upload happy/missing/unknown/oversize (413), download (binary + Content-Disposition), list, bundle ZIP (verificato letto via `archive/zip`, dedup nomi duplicati).

### JS (`node:test`)

- `blocks.test.mjs`: parser blocchi (input vuoto, blocchi singoli/multipli/spaiati/vuoti, whitespace, righe contenenti `-----`).
- `countdown.test.mjs`: formattazione `MM:SS`/`HH:MM:SS`, `msUntil`, `isExpired` (persistente = mai scaduto).
- `download.test.mjs`: `safeFilename` (caratteri proibiti, controllo, truncate 80, fallback), `buildFilename` (slug/kind/index, sanitize, defaults).
- `files.test.mjs`: `parseFileMarker` (valid, encoded, whitespace, inline rejection, malformed, non-string), `buildFileMarker` (roundtrip, fallback), `formatBytes`.

---

## Docker

```bash
# Via compose (consigliato)
just up        # build + start + volume named
just smoke     # curl di healthz, create, put, get
just down      # stop + drop container

# Senza compose
docker build -t sharetext .
docker run --rm -p 8080:8080 -v $(pwd)/data:/data sharetext
```

L'image è basata su `gcr.io/distroless/static:nonroot`. Volume `/data` preallocato con owner `nonroot:nonroot` (UID 65532) nello stage build, quindi al primo mount di un volume vuoto Docker eredita correttamente le perms (no permission denied su SQLite open).

Build args:

- `VERSION` — override `internal/version.Version`.

Env (vedi tabella sopra) sono passabili via `-e` o `compose.yaml`.

---

## Struttura

```
cmd/server/
  main.go                  bootstrap, env parsing, route mount, cleanup goroutine
  templates/
    index.html             landing (form a due modalità)
    session.html           pagina sessione (editor + righe + toolbar)
    admin.html             pannello admin
  static/
    style.css              CSS unico (desktop + mobile + admin)
    app.js                 client sessione: WS, debounce, render, drag-drop, toggle mobile
    blocks.js              parser blocchi (`-----`)
    countdown.js           helpers countdown (formattazione, msUntil, isExpired)
    download.js            helpers download client-side (sanitize filename, blob trigger)
    files.js               parser marker file, builder marker, formatBytes
    admin.js               client admin: list, delete, mobile cards
    blocks.test.mjs        node:test
    countdown.test.mjs     node:test
    download.test.mjs      node:test
    files.test.mjs         node:test
internal/
  session/                 slug crypto-random + validazione nome
  store/
    store.go               sessions CRUD + scadenza + DeleteExpired + ListActive
    files.go               files CRUD + ReferencedFileIDs + DeleteOrphanFiles
  handlers/
    api.go                 CRUD sessioni HTTP
    ws.go                  WebSocket handler
    hub.go                 pub/sub in-memory per stanza
    admin.go               basic auth + list + delete
    files.go               upload + download + list + bundle ZIP
  version/
    version.go             Version var (ldflags-overridable)
Dockerfile                 multi-stage, distroless, ARG VERSION
compose.yaml               servizio con volume + build-arg
Justfile                   ricette (run, build, test*, up, down, smoke)
README.md                  questo file
```
