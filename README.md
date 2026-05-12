# sharetext

Webapp minimale per condividere snippet di testo in tempo reale. Backend Go, persistenza SQLite, sync via WebSocket, frontend vanilla JS.

## Caratteristiche

- Due modalità di sessione:
  - **Persistente**: nome obbligatorio (regex `^[A-Za-z0-9_-]{1,32}$`), preposto allo slug random. Resta finché non viene rimossa.
  - **Temporanea**: durata obbligatoria in minuti (1..10080). Allo scadere viene cancellata in modo definitivo dal DB (hard delete via goroutine periodica).
- Slug univoco crypto-random; per le persistenti la forma finale è `{nome}-{random}`.
- Chiunque conosce il link può leggere e scrivere.
- Sync in tempo reale tra client (WebSocket), con copia automatica dello stato iniziale alla connessione.
- Countdown visibile in pagina durante una sessione temporanea; al raggiungimento dello zero overlay "Sessione scaduta" e disconnessione.
- Copia tutto, copia singola riga, copia blocco multi-riga (vedi [Formato blocchi](#formato-blocchi)).
- Persistenza su SQLite (WAL).

## Avvio rapido

```bash
go run ./cmd/server
# oppure
just run
```

Apri http://localhost:8080. Scegli **Persistente** + nome, oppure **Temporanea** + durata in minuti.

### Variabili d'ambiente

| Variabile           | Default        | Descrizione                                           |
|---------------------|----------------|-------------------------------------------------------|
| `PORT`              | `8080`         | Porta HTTP                                            |
| `DB_PATH`           | `sharetext.db` | File SQLite                                           |
| `SLUG_LEN`          | `16`           | Lunghezza della parte random dello slug               |
| `CLEANUP_INTERVAL`  | `30s`          | Frequenza sweep cancellazione sessioni scadute        |

## API

### `POST /api/sessions`

Crea una sessione. Il body è JSON obbligatorio:

```jsonc
// Persistente
{ "type": "persistent", "name": "team-alpha" }

// Temporanea (60 minuti)
{ "type": "temporary",  "minutes": 60 }
```

Validazione:

- `type` deve essere `persistent` o `temporary`.
- `name`: 1–32 caratteri, solo `[A-Za-z0-9_-]`. Obbligatorio per `persistent`, rifiutato (ignorato) per `temporary`.
- `minutes`: intero in `[1, 10080]`. Obbligatorio per `temporary`.

Risposta `201 Created`:

```jsonc
{
  "slug": "team-alpha-3vRdM58dftguriSe",
  "url": "/s/team-alpha-3vRdM58dftguriSe",
  "name": "team-alpha",
  "expires_at": null
}
```

Per le temporanee `name` è omesso e `expires_at` è un timestamp RFC3339 in UTC.

Errori: `400 Bad Request` su validazione, `500` su errore interno.

### `GET /api/sessions/{slug}`

```jsonc
{
  "slug": "...",
  "name": "team-alpha",            // omesso per le temporanee
  "content": "...",
  "updated_at": "2026-05-12T14:00:00Z",
  "expires_at": "2026-05-12T15:00:00Z"  // omesso per le persistenti
}
```

Codici: `200` ok, `404` sconosciuto, `410 Gone` quando la sessione è scaduta (anche prima dello sweep).

### `PUT /api/sessions/{slug}`

Body `{"content": "..."}`. Stessi codici di GET (`200/400/404/410`). In caso di successo broadcast su tutti i WebSocket attivi della stessa stanza.

### `GET /ws/{slug}` (WebSocket)

Messaggi JSON `{"content": "..."}` in entrambe le direzioni. Il server invia lo stato iniziale alla connessione e fa broadcast a tutti i peer ad ogni update. Connessione rifiutata con `404` se lo slug è inesistente o scaduto.

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
```

## Formato blocchi

Nell'editor un gruppo di righe può essere marcato come **blocco** racchiudendolo tra due righe contenenti esattamente `-----` (ammessi spazi a inizio/fine). Nel pannello a destra il blocco viene mostrato come **un'unica voce** con un solo pulsante "Copia blocco" che copia il contenuto interno (delimitatori esclusi).

Esempio:

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

- I delimitatori (`-----`) non sono inclusi nel testo copiato del blocco.
- Un delimitatore "spaiato" (numero dispari di `-----`) resta una riga normale.
- Due `-----` consecutivi formano un blocco vuoto.
- Una riga che *contiene* `-----` ma non è composta solo da `-----` (con eventuali spazi) NON è un delimitatore.
- Il pulsante **Copia tutto** copia il contenuto raw dell'editor, delimitatori compresi (preserva round-trip).

## Sessioni temporanee — comportamento di scadenza

- `expires_at` viene calcolato server-side al `POST /api/sessions` come `now + minutes*60s` (UTC).
- Ogni `GET`/`PUT`/`WS` controlla l'`expires_at` correntemente in DB: una sessione scaduta restituisce `410 Gone` anche prima che il job di cleanup la rimuova.
- Una goroutine eseguita ogni `CLEANUP_INTERVAL` (default 30s) lancia `DELETE FROM sessions WHERE expires_at <= now()` — **hard delete, non recuperabile**.
- In UI:
  - countdown live nell'header con formato `MM:SS` (o `HH:MM:SS` oltre l'ora);
  - colore arancione nell'ultimo minuto;
  - allo zero appare un overlay "Sessione scaduta", editor disabilitato, WebSocket chiuso.

## Test

```bash
just test-all      # Go + JS
# oppure separati:
go test ./...
node --test cmd/server/static/blocks.test.mjs cmd/server/static/countdown.test.mjs
```

Coperti:
- **Go**:
  - `internal/session`: generatore slug, regex nome (validi/invalidi/edge case lunghezza), composizione `Compose(name)`.
  - `internal/store`: CRUD, duplicati, concorrenza, `expires_at` (Get/Update restituiscono `ErrExpired`), `DeleteExpired` (hard delete con scenari misti), `Delete`.
  - `internal/handlers`: API HTTP (httptest) — persistente con/senza nome valido, temporanea con/senza minutes validi, 410 su sessione scaduta, hub broadcast, WebSocket end-to-end.
- **JS** (`node:test`):
  - parser blocchi (`blocks.test.mjs`): input vuoto, blocchi singoli/multipli/spaiati/vuoti, edge case whitespace.
  - countdown (`countdown.test.mjs`): formattazione `MM:SS`/`HH:MM:SS`, `msUntil`, `isExpired` (incluso persistente = mai scaduto).

## Docker

```bash
just up        # build + run via compose
just smoke     # smoke test endpoints
just down

# senza compose
docker build -t sharetext .
docker run --rm -p 8080:8080 -v $(pwd)/data:/data sharetext
```

## Struttura

```
cmd/server/        main, cleanup goroutine, embed di template e asset statici
  templates/       HTML server-rendered (index, session)
  static/          JS modules (app, blocks, countdown), CSS
internal/session/  generatore slug + validazione nome
internal/store/    SQLite store (CRUD, scadenza, cleanup)
internal/handlers/ API REST, hub WebSocket
```
