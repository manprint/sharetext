# sharetext

Webapp per condividere snippet di testo (e file) in tempo reale.
Backend Go single-binary, persistenza SQLite, sync via WebSocket, frontend vanilla JS.

## Indice

- [Caratteristiche](#caratteristiche)
- [Avvio rapido](#avvio-rapido)
- [Sicurezza & E2E](#sicurezza--e2e)
- [Variabili d'ambiente](#variabili-dambiente)
- [API sessioni](#api-sessioni)
- [Allegati (file)](#allegati-file)
- [Formato blocchi](#formato-blocchi)
- [Lock di modifica](#lock-di-modifica)
- [Comandi editor](#comandi-editor)
- [Vista righe & UI mobile](#vista-righe--ui-mobile)
- [PWA & offline](#pwa--offline)
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
- Stato iniziale inviato sulla connessione (incluso un `client_id` opaco e lo stato del lock di modifica).
- Last-write-wins, ma scrittura **mutuamente esclusiva**: vedi [Lock di modifica](#lock-di-modifica).

**Editor**

- Textarea + pannello "righe" affiancato (desktop) o impilato (mobile, single column).
- **Copia/Scarica per riga**, **Copia/Scarica per blocco multi-riga**, **Copia/Scarica tutto** in toolbar.
- Sintassi blocchi `-----` per raggruppare più righe in un'unica voce.
- Countdown live nell'header per sessioni temporanee; overlay "Sessione scaduta" allo zero.

**Allegati**

- Upload via bottone (selettore file, inserisce alla posizione corrente del cursore) o **drag & drop** sull'editor (inserisce nella riga dove avviene il drop).
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

**Sicurezza**

- **Cifratura end-to-end** opzionale per ogni nuova sessione: chiave AES-256-GCM generata nel browser, distribuita via fragment URL `#k=…` (mai inviato al server). Vedi [Sicurezza & E2E](#sicurezza--e2e).
- **WebSocket origin check** configurabile (`ALLOWED_ORIGINS`); default same-origin.
- **HSTS** abilitato di default quando il proxy segnala HTTPS.
- **Admin password con hash bcrypt** (`ADMIN_PASS_HASH`), preferito sul plaintext `ADMIN_PASS`.
- **`PRAGMA secure_delete=ON`** per evitare residui leggibili nelle pagine libere SQLite.
- **CSP stretta** di default (no `'unsafe-inline'` su `script-src`), `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Permissions-Policy` minimale. Link nel testo emessi con `rel="noopener noreferrer"` per non leakare il fragment.
- **Rate limit** per-IP separato per route pubblici, admin e creazione sessioni (`POST /api/sessions`, bucket dedicato più stretto contro flood).
- **WebSocket read deadline** (`WS_READ_TIMEOUT`, default `90s`) chiude le connessioni idle e protegge da slow-loris su upgrade.
- **Bcrypt admin hash validato a startup**: hash malformato → fail-fast, niente silent disable.

**PWA**

- Installabile come app: manifest, icone, service worker.
- Service worker cache-first per asset statici, network-first per shell HTML, stale-while-revalidate per snapshot/listing, cache-first per blob file.
- Banner "Offline — sola lettura" + offline guard che blocca scrittura e disabilita upload quando il network è down.
- Cache versionata per `version.Version`: nuovo deploy ⇒ cache invalidata su `activate`.

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

## Sicurezza & E2E

### Cifratura end-to-end (sessioni create dopo l'introduzione di questa feature)

- Dalla landing, ogni nuova sessione genera una chiave AES-256-GCM nel browser del creator. La chiave viene appesa al URL dopo `#` (es. `https://host/s/abc-xyz#k=…`). I browser **non** inviano il fragment al server in nessuna richiesta HTTP, quindi il server vede solo ciphertext.
- Il contenuto testuale viene serializzato come `enc:v1:<iv-b64url>:<ciphertext-b64url>`. I file vengono cifrati come bytes (IV prepended, payload binario). Il nome file viene cifrato separatamente in formato `<iv>.<ct>` e va a riempire il marker `[file:<id>:<name-cifrato>]`.
- Aprire la sessione **senza** il fragment `#k=…` la mette in modalità "sola lettura cifrata": banner di avviso, editor read-only, niente render del ciphertext grezzo.
- Sessioni create **prima** dell'introduzione restano plaintext: nessuna migrazione automatica (impossibile senza chiave). Per cifrare contenuti vecchi: creare una nuova sessione e fare copy/paste manuale.
- **Threat model**: la cifratura protegge da dump del DB, snapshot del disco VPS, accesso operatore al filesystem o al backup. **Non** protegge se l'operatore può sostituire `app.js` con codice malevolo (limite intrinseco di tutti gli E2E in web app), né da chi riceve il link completo.
- "Copia link" copia `location.href` incluso il fragment → chi riceve può leggere. Cronologia browser, bookmark sync, screenshot esporrebbero la chiave: trattare il link come dato sensibile.
- Bundle ZIP: per le sessioni E2E il bottone "Scarica" produce uno zip plaintext assemblato lato browser (testo + file decifrati). Per sessioni legacy resta il bundle server-side.

### Hardening server

- **WebSocket origin check**: l'accept rifiuta richieste cross-origin. Configura `ALLOWED_ORIGINS` (CSV) per autorizzare host esterni; vuoto = solo same-origin.
- **HSTS attivo by default** dietro reverse proxy che inoltra `X-Forwarded-Proto: https` (vedi `STRICT_TRANSPORT_SECURITY`).
- **Admin con hash bcrypt**: preferire `ADMIN_PASS_HASH` su `ADMIN_PASS`. Genera con:
  ```bash
  go run ./scripts/bcrypt-hash 'la-tua-password'
  # oppure
  htpasswd -bnBC 12 "" 'la-tua-password' | tr -d ':\n'
  ```
- **`PRAGMA secure_delete=ON`** (env `SECURE_DELETE`, default `true`): zera le pagine libere SQLite invece di lasciare residui leggibili.
- **CSP stretta** (default sgancia `'unsafe-inline'` da `script-src`): se passi una CSP custom via env, includere `worker-src 'self'; manifest-src 'self'` per non rompere SW + manifest PWA.

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
| `LOCK_TTL` | `15s` | TTL del lock di modifica editor. Un client che detiene il lock deve inviare un heartbeat entro questo intervallo, altrimenti il server libera il lock e un altro utente puo' acquisirlo. Minimo accettato: `1s`. |
| `LOCK_IDLE_RELEASE` | `3s` | Tempo di inattività dopo cui il client che detiene il lock lo rilascia spontaneamente, così che un altro utente possa prendere il turno. Il valore viene servito al browser via attributo `data-idle-release` su `<body>`. Minimo accettato: `1s`; valori più piccoli vengono ignorati e si ricade sul default. |
| `WS_READ_TIMEOUT` | `90s` | Tempo massimo che il server attende un nuovo frame WebSocket prima di chiudere la connessione. Difende da slow-loris-like su connessioni upgraded: una connessione idle senza heartbeat o input viene chiusa quando il timeout scatta. Minimo accettato: `1s`. Tipicamente non serve toccarlo: i client `app.js` inviano heartbeat ogni `LOCK_TTL/2`, ben sotto il default. |
| `MAX_FILE_SIZE` | `10485760` | Limite massimo upload per singolo file in byte. Default 10 MiB. |
| `MAX_CONTENT_SIZE` | `6291456` | Limite massimo del contenuto testuale di sessione in byte. Vale per `PUT` e WebSocket. Il default (6 MiB) lascia spazio all'inflazione ~34% del ciphertext AES-GCM base64, mantenendo la capacità plaintext effettiva attorno ai 4 MiB. |
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
| `CREATE_RATE_LIMIT_ENABLED` | `true` | Abilita il rate limiter dedicato a `POST /api/sessions` (in aggiunta al rate limiter pubblico). Difende dal flood di creazione sessioni che riempirebbe rapidamente il volume DB. Si disattiva automaticamente anche se `RATE_LIMIT_ENABLED=false`. |
| `CREATE_RATE_LIMIT_RPS` | `1` | Token/sec del rate limit dedicato a `POST /api/sessions`. |
| `CREATE_RATE_LIMIT_BURST` | `5` | Burst massimo del rate limit di creazione sessioni: un IP può creare 5 sessioni in rapida successione, poi è capped a 1/s. |
| `CREATE_RATE_LIMIT_TTL` | `10m` | TTL degli entry IP del rate limiter di creazione. |
| `READ_HEADER_TIMEOUT` | `5s` | Timeout di lettura degli header HTTP. |
| `WRITE_TIMEOUT` | `30s` | Timeout di scrittura della risposta HTTP. |
| `IDLE_TIMEOUT` | `2m` | Timeout keep-alive HTTP. |
| `MAX_HEADER_BYTES` | `1048576` | Limite massimo per gli header HTTP. |
| `SECURITY_HEADERS_ENABLED` | `true` | Abilita CSP e altri header di hardening HTTP. |
| `CONTENT_SECURITY_POLICY` | vedi default | Valore completo dell'header `Content-Security-Policy`. |
| `FRAME_OPTIONS` | `DENY` | Valore di `X-Frame-Options`. |
| `REFERRER_POLICY` | `no-referrer` | Valore di `Referrer-Policy`. |
| `PERMISSIONS_POLICY` | `camera=(), microphone=(), geolocation=()` | Valore di `Permissions-Policy`. |
| `STRICT_TRANSPORT_SECURITY` | `max-age=31536000; includeSubDomains` | Header `Strict-Transport-Security` inviato quando la richiesta è HTTPS o il proxy segnala `X-Forwarded-Proto: https`. Imposta a stringa vuota per disabilitare. |
| `ALLOWED_ORIGINS` | _(unset)_ | Lista comma-separated di origin autorizzati a parlare con il WebSocket. Vuoto = solo same-origin (Host header). Se la CSP custom è stretta, ricordare di includere `worker-src 'self'; manifest-src 'self'`. |
| `SECURE_DELETE` | `true` | Attiva `PRAGMA secure_delete=ON` sul DB: le righe cancellate vengono azzerate nelle pagine libere invece di restare leggibili fino alla riscrittura. |
| `METRICS_ENABLED` | `true` | Abilita la raccolta di metriche operative in memoria e l'endpoint admin dedicato. |
| `AUDIT_LOG_ENABLED` | `true` | Abilita la persistenza degli audit log admin. |
| `AUDIT_LOG_DEFAULT_LIMIT` | `50` | Limite di default di `/admin/api/audit` quando manca `?limit=`. |
| `ADMIN_USER` | _(unset)_ | Username Basic Auth per `/admin`. Se vuoto, admin disabilitato (503). |
| `ADMIN_PASS` | _(unset)_ | Password Basic Auth per `/admin` (plaintext, legacy). Se vuota e `ADMIN_PASS_HASH` non è impostata, admin disabilitato (503). |
| `ADMIN_PASS_HASH` | _(unset)_ | Hash bcrypt della password admin (preferito su `ADMIN_PASS`). Se entrambi impostati, l'hash vince. Genera con `go run sharetext/scripts/bcrypt-hash` o `htpasswd -bnBC 12 "" 'pass'`. |

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

Body `{"content": "..."}`. Stessi codici (`200/400/404/410`) piu' `413` se `content` supera `MAX_CONTENT_SIZE` oppure la quota totale della sessione (`MAX_SESSION_STORAGE_BYTES`), e `409 Conflict` se il lock di modifica e' detenuto da un altro client (vedi [Lock di modifica](#lock-di-modifica)). Sul successo il server fa broadcast su tutti i WebSocket attivi della stessa stanza.

Header opzionale `X-Client-ID` (il valore deve corrispondere al `client_id` ottenuto sulla WebSocket): identifica il client come potenziale detentore del lock. La PUT acquisisce automaticamente il lock per quel client se libero, lo rinnova se gia' detenuto dal client stesso, e ritorna `409` se detenuto da un altro. Senza header, la PUT e' permessa solo quando il lock e' libero.

### `GET /ws/{slug}` — WebSocket

Stream JSON bidirezionale. Connessione rifiutata con `404` se lo slug non esiste o e' scaduto. Messaggi oltre `MAX_CONTENT_SIZE` chiudono la connessione con close status `1009` (`message too big`).

**Server → client** (primo messaggio dopo il connect):

```jsonc
{
  "slug": "...",
  "content": "...",
  "updated_at": "...",
  "expires_at": "...",  // solo per sessioni temporanee
  "client_id": "abc123",        // identificativo client effimero
  "lock": { "held": false }     // stato lock di modifica
}
```

Successivi `session payload` (broadcast su edit) hanno lo stesso shape ma senza `client_id`. Eventi tipizzati:

- `{"type": "lock", "lock": {...}}` — emesso quando lo stato del lock cambia (acquisito, rilasciato, transito di holder).
- `{"type": "lock_denied", "lock": {...}}` — inviato solo al chiamante quando un suo write/acquire e' stato rifiutato perche' il lock e' di un altro utente. Il payload `lock` riporta il detentore attuale.

**Client → server**:

| Messaggio | Effetto |
|-----------|---------|
| `{"type": "edit", "content": "..."}` (o legacy `{"content": "..."}`) | Acquisisce auto-il lock per il `client_id` corrente e applica il contenuto. Rifiutato con `lock_denied` se un altro client detiene il lock. |
| `{"type": "lock_acquire"}` | Richiesta esplicita di acquisizione. Idempotente per il detentore. Rifiutata con `lock_denied` se occupato. |
| `{"type": "lock_heartbeat"}` | Rinnova il TTL del lock. Inviato dal client che detiene il lock ad intervalli `<= LOCK_TTL/2`. Rifiutato con `lock_denied` se il chiamante non e' il detentore. |
| `{"type": "lock_release"}` | Rilascia il lock se chi parla e' il detentore. No-op altrimenti. Trigger un evento `lock` con `held: false`. |

Sul disconnect (qualsiasi causa) il server rilascia automaticamente l'eventuale lock detenuto dal client e fa broadcast dello stato libero.

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

- **Bottone Upload** (header): apre selettore file, inserisce i marker alla **posizione corrente del cursore** nella textarea (`$content.selectionStart` viene catturato prima di aprire la dialog, così il valore sopravvive al blur). Quando la textarea non è mai stata focalizzata i marker vengono accodati a fine testo. I marker restano sempre su riga intera (vincolo del parser `FILE_RE`): viene anteposto un `\n` solo se il cursore non era a inizio riga, e un `\n` di chiusura solo se il testo successivo non inizia già con `\n`. In questo modo non vengono introdotte righe vuote di troppo: inserire il marker subito prima di un newline esistente lascia esattamente *una* riga marker fra le due porzioni di testo.
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

## Lock di modifica

Le operazioni di **scrittura** (edit testuale, upload file, drag&drop) sono mutuamente esclusive tra gli utenti collegati alla stessa sessione. Le operazioni di sola lettura (copia, scarica, vista righe, download del bundle) restano sempre disponibili.

### Modello

- Ogni connessione WebSocket riceve un `client_id` opaco generato dal server (16 caratteri base58-ish).
- Un solo `client_id` alla volta puo' detenere il **lock di modifica** di una sessione. Tutti gli altri vedono l'editor in **sola lettura grigio** con un badge `🔒 in modifica` e tooltip esplicito.
- Il lock viene **acquisito automaticamente** al primo write (testo o upload) eseguito da un client identificato (header `X-Client-ID` per HTTP, `client_id` di connessione per WS). Non serve un round-trip esplicito: il cliente puo' anche emettere `{"type":"lock_acquire"}` per acquisire il lock in modo ottimistico prima che parta il primo input (cosi' i peer vedono immediatamente l'editor grigio).
- Il lock ha **TTL** configurabile via `LOCK_TTL` (default `15s`). Il detentore invia un `lock_heartbeat` ad intervalli `<= LOCK_TTL/2` per rinnovarlo.
- Il client rilascia il lock spontaneamente dopo `LOCK_IDLE_RELEASE` di inattività (default `3s`, configurabile via env; vedi [Variabili d'ambiente](#variabili-dambiente)) emettendo `lock_release`, oppure quando l'utente chiude la pagina/tab (`beforeunload`). Il server rilascia il lock anche alla chiusura della WebSocket (per qualsiasi motivo: navigazione, network blip, kill della tab).

### Garanzie

- **Mutua esclusione**: il `LockManager` server-side serializza acquisizione/heartbeat/release sotto un singolo `sync.Mutex`. Due richieste concorrenti vedono esiti coerenti, mai entrambe "granted".
- **Anti-stallo**: il TTL evita che un client morto blocchi la sessione. Se un client perde connettivita' senza chiudere pulitamente, il lock si libera in `<= LOCK_TTL` e il prossimo writer puo' acquisire.
- **Server source-of-truth**: il client si fida sempre dell'ultimo `lock` event ricevuto. Se un client locale crede di detenere il lock ma il server l'ha gia' rilasciato (es. heartbeat saltato per throttling background-tab), il prossimo write tornera' `lock_denied` e l'UI si riallinea.
- **Rollback su denial**: se un client perde il lock mentre ha modifiche locali in flight, l'editor viene ripristinato all'ultimo contenuto confermato dal server (per evitare ghost text che i peer non vedono mai).

### Codici di errore

- `HTTP 409 Conflict` su `PUT /api/sessions/{slug}` o `POST /api/sessions/{slug}/files` quando un altro client detiene il lock. Body JSON: `{"error":"editor locked by another user","lock":{...}}`.
- WS `{"type":"lock_denied","lock":{...}}` come equivalente sulla socket.

### Compatibilita' legacy

I client che ignorano l'header `X-Client-ID` e i messaggi tipizzati continuano a funzionare quando il lock e' libero: la PUT/POST procede in modalita' "anonima" senza acquisire ownership. Diventa un `409` solo quando un altro utente sta gia' editando.

---

## Comandi editor

L'editor riconosce **slash command** inline. In qualsiasi punto del testo, digitando `/` si apre un **dropdown autocomplete** con tutti i comandi disponibili; continuando a digitare la lista si filtra per prefisso. Il comando selezionato sostituisce il token `/nome` esattamente al cursore.

### Rilevamento token

- Pattern: `/` seguito da zero o più caratteri `[a-zA-Z0-9_-]`, con il caret immediatamente dopo l'ultimo carattere del nome.
- Il `/` deve trovarsi a inizio buffer oppure essere preceduto da whitespace (` `, tab, `\n`, `\r`). Questo evita falsi positivi su URL (`https://...`) e path inline.
- Il caret deve essere *alla fine* del token: se l'utente sposta il cursore in mezzo a un comando già scritto, il dropdown non si apre.
- Funziona ovunque nel testo: inizio riga, mid-line, dopo un blocco `-----`, prima/dopo un marker file.

### Dropdown

| Tasto         | Effetto |
|---------------|---------|
| `↑` / `↓`     | Naviga le voci. |
| `Enter` o `Tab` | Esegue la voce evidenziata. |
| `Esc`         | Chiude il dropdown senza eseguire. |
| Click su voce | Esegue quella voce (il focus resta sulla textarea). |

Il dropdown si chiude automaticamente quando:

- il caret si sposta fuori dal token (frecce, click, selezione altrove);
- l'utente digita un carattere non-name (spazio, punteggiatura, newline);
- nessun comando registrato corrisponde al prefisso;
- la textarea perde il focus;
- il [lock di modifica](#lock-di-modifica) passa ad altro utente.

### Comandi disponibili

| Comando      | Effetto |
|--------------|---------|
| `/timestamp` | Sostituisce esattamente il token `/timestamp` con la data e ora locale correnti nel formato `DD-MM-YYYY_HH-MM-SS`. Caret posizionato a fine timestamp; il testo circostante non viene toccato. |
| `/upload`    | Sostituisce il token `/upload` con i marker `[file:<id>:<url-encoded-name>]` dei file selezionati (uno per file, su righe nuove). Se il `/` non era a inizio riga viene inserito un newline prima dei marker per rispettare il vincolo "marker su riga intera". Se l'utente annulla la dialog, il token resta semplicemente rimosso. |

Pulsante Upload dell'header e drag & drop usano lo stesso meccanismo di inserzione: il bottone inserisce alla **posizione corrente del cursore** (con fallback a fine testo se la textarea non è mai stata focalizzata), drag & drop continua a inserire all'inizio della riga in cui è avvenuto il drop. Tutti e tre i flussi (`/upload`, bottone, drag & drop) condividono `insertMarkersAtPosition`, che antepone un `\n` quando l'offset non è a inizio riga per preservare l'invariante "marker su riga intera".

### Garanzie

- Il dispatch è gated dal [lock di modifica](#lock-di-modifica): se un altro utente detiene il lock, il dropdown non compare e l'esecuzione è bloccata insieme al resto della scrittura.
- Le modifiche prodotte dai comandi viaggiano sullo stesso percorso di un edit utente: render locale → debounce → WS `edit` (con auto-acquisizione del lock) → broadcast ai peer.
- Per `/upload` il flusso di upload riusa `POST /api/sessions/{slug}/files` + `PUT /api/sessions/{slug}` esistente, quindi i limiti di quota (`MAX_FILE_SIZE`, `MAX_FILES_PER_SESSION`, `MAX_SESSION_STORAGE_BYTES`) e i 409 di lock sono propagati come per gli altri flussi.

### Estendere con un nuovo comando

Il registry vive in `cmd/server/static/commands.js`. È sufficiente importare `registerCommand` da `app.js` (o da un nuovo modulo importato da `app.js`) e registrare un handler:

```js
import { registerCommand } from './commands.js';

registerCommand('shout', (ctx) => {
  // ctx = { name, args, text, caret, tokenStart, tokenEnd }
  // Sostituisci ctx.text.slice(ctx.tokenStart, ctx.tokenEnd) col tuo output
  // e applica via applyProgrammaticEdit(nextValue, newCaret).
});
```

L'handler può essere `async` (il dispatcher attende la `Promise`). Le primitive utili esposte da `commands.js` sono `findSlashTokenAtCaret`, `filterCommands`, `formatTimestamp` — tutte pure function coperte da `commands.test.mjs`.

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

## PWA & offline

L'app è una Progressive Web App installabile: manifest, icone, service worker e shell cacheata permettono apertura offline e behavior consistente quando la rete è instabile.

### Manifest + icone

- `static/manifest.webmanifest` con `name`, `short_name`, `theme_color: #0f766e`, `background_color`, `display: standalone`.
- Icone `icon-192.png`, `icon-512.png`, `icon-maskable.svg`, `apple-touch-icon` (`icon-180.png`).
- Indirizzo manifest registrato in `<head>` di landing e session. Browser compatibili offrono "Aggiungi alla schermata Home".

### Service worker

- Generato a partire da `cmd/server/sw.js.tmpl`, servito su `/sw.js` con `Service-Worker-Allowed: /` e `Cache-Control: no-cache` (il byte-diff su deploy nuovo triggera install+activate del nuovo SW).
- Cache name versionata su `internal/version.Version`: ogni release evict completamente le cache precedenti in `activate`.
- Strategie di routing (definite in `static/sw-routes.js`, riusabili e testate):
  - **Asset statici** (`/static/*` ad esclusione di `sw.js` e `manifest.webmanifest`) → cache-first.
  - **Shell HTML** (`/`, `/s/{slug}`) → network-first con fallback alla shell cacheata.
  - **Snapshot sessione** (`GET /api/sessions/{slug}`) → stale-while-revalidate.
  - **Listing file metadata** (`GET /api/sessions/{slug}/files`) → stale-while-revalidate.
  - **Download file binario** (`GET /api/sessions/{slug}/files/{id}`) → cache-first blob.
  - **Bundle ZIP**, **WebSocket upgrade**, **healthz**, **admin**, **scritture (POST/PUT/DELETE)** → passthrough, mai cacheate.
- Le richieste non classificate restano passthrough; il routing è puro JS testato in `sw-routes.test.mjs` (oltre 15 casi).

### Offline guard lato client

- Listener su `navigator.onLine` + ping periodico → banner "Offline — sola lettura" appeso in cima alla session.
- Quando offline: editor diventa read-only (`readOnly = true`), upload e bottoni di scrittura disabilitati, debounce locale interrotto, riconciliazione differita alla riconnessione.
- Module pure `offline-guard.js` con stato gestito a init time, transizioni testate in `offline-guard.test.mjs` (coerce truthy/falsy, ritorno booleano solo sui cambi di stato effettivi).

### Cifratura ed E2E

Le risorse cacheate dal service worker sono opache: testo cifrato resta cifrato anche nella cache. La decifratura avviene client-side post-`fetch`/post-`match`, quindi il SW non altera né legge il plaintext.

**Invalidazione automatica della cache su deploy.** Il SW cachea `app.js`, `crypto.js`, `e2e-state.js` con strategia *cache-first* (vedi `sw-routes.js`). Il nome delle cache è `{kind}-{Version}-{BuildID}`, dove `BuildID` è un hash SHA-256 calcolato a startup su **tutti i file embeddati** (statici + template). Anche riutilizzando lo stesso tag `VERSION` su deploy successivi (tipico in dev), ogni modifica a un file statico cambia il `BuildID` → cambia il nome cache → `activate` evicta le cache vecchie → i client riscaricano il bundle. Nessuna perdita dati: le cache `api-` e `files-` sono solo copie temporanee di risposte server (i dati canonici vivono in SQLite), quindi il next online fetch ripopola.

Bumpare `internal/version/version.go` resta utile per **etichettare** la release in UI/log; non è più necessario per invalidare la cache.

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
just vuln           # scansiona dipendenze con govulncheck (installa on-demand)
just vet            # go vet
```

### Go

- `cmd/server` (config loader): override env, fallback su valori invalidi, default `LOCK_TTL` / `LOCK_IDLE_RELEASE` / `WS_READ_TIMEOUT` (con rifiuto valori sotto 1s), defaults e override del bucket `CREATE_RATE_LIMIT_*`.
- `cmd/server` (build id): `computeBuildID` deterministico sugli asset embeddati; due chiamate sullo stesso FS producono lo stesso digest, una variazione di contenuto produce un digest diverso (pinato via fixture in-tree).
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
  - `DeleteOrphanFiles`: unreferenced + grace protection + safety-net fallback con FK off;
  - `secure_delete` PRAGMA letto correttamente quando l'opzione è attiva.
- `internal/handlers`:
  - API sessioni (httptest): persistent/temporary validi e invalidi (400), 410 su scaduta su GET e PUT;
  - hub broadcast / except / leave;
  - WebSocket end-to-end con 2 client + slug inesistente + origin foreign rifiutato (403) + same-host accettato (101) + read deadline che chiude la connessione idle (`WS_READ_TIMEOUT`);
  - admin: missing/wrong creds (401), creds vuote (503), list (200 con esclusione scaduti, total_size con files), delete (200 + 404 idempotenza), delete unauthorized; branch bcrypt (`ADMIN_PASS_HASH`) valido e invalido, timing-safe;
  - middleware: HSTS applicato su `X-Forwarded-Proto: https`; rate limiter blocca burst per-IP, scenario dedicato `POST /api/sessions` (`CREATE_RATE_LIMIT_*`) per resistenza al flood;
  - file: upload happy/missing/unknown/oversize (413), download (binary + Content-Disposition), list, bundle ZIP (verificato letto via `archive/zip`, dedup nomi duplicati);
  - lock: acquire/release/heartbeat, denial cross-client, auto-release su disconnect.

### JS (`node:test`)

- `blocks.test.mjs`: parser blocchi (input vuoto, blocchi singoli/multipli/spaiati/vuoti, whitespace, righe contenenti `-----`).
- `countdown.test.mjs`: formattazione `MM:SS`/`HH:MM:SS`, `msUntil`, `isExpired` (persistente = mai scaduto).
- `download.test.mjs`: `safeFilename` (caratteri proibiti, controllo, truncate 80, fallback), `buildFilename` (slug/kind/index, sanitize, defaults).
- `files.test.mjs`: `parseFileMarker` (valid, encoded, whitespace, inline rejection, malformed, non-string), `buildFileMarker` (roundtrip, fallback), `formatBytes`, `extractMarkerIds` (set diff su markers — usato dal refresh meta on-peer-upload).
- `commands.test.mjs`: `findSlashTokenAtCaret` (token a fine buffer / dopo whitespace / a inizio riga, prefissi parziali, URL e path interni rifiutati, caret in mezzo a un token, clamp out-of-range, non-string), `filterCommands` (match prefisso case-insensitive), `formatTimestamp` (zero padding, valori massimi, default `now`), registry (`registerCommand` validazione + handler async, `dispatchCommand` happy/unknown/missing-ctx).
- `crypto.test.mjs`: roundtrip `encryptText`/`decryptText`, unicità IV su due encrypt dello stesso plaintext, ciphertext-su-empty-string conserva prefisso `enc:v1:`, tampering byte → `OperationError`, roundtrip binario via `encryptBytes`/`decryptBytes`, encoding nome cifrato (`encryptName`/`decryptName`), `isCiphertext`/`isEncryptedName` discrimination.
- `bundle-client.test.mjs`: builder ZIP STORE method (no compressione) per sessioni E2E — build + parsing entries via `DecompressionStream` di un consumer-side, dedup nomi duplicati, bytes integrity.
- `e2e-state.test.mjs`: state machine pura della cifratura (decideInitialMode, classifyIncoming, isSafePlaintext). Pinning specifico contro la regressione "ciphertext nell'editor" che si è verificata in produzione (8 casi di transizione + guardia anti-leak su valori che iniziano per `enc:v1:`). Garantisce che il path di rendering rifiuti qualunque stringa cifrata anche se un futuro bug aggirasse la decifratura.
- `linkify.test.mjs`: rendering anchor con `rel="noopener noreferrer" target="_blank"`, scheme http/https only, evita falsi positivi e URL troncati.
- `lock.test.mjs`: stati `classifyLock`, `canEditNow`, `shouldRequestLock`, `nextHeartbeatDelayMs` (half-TTL clamp min/max, fallback senza expiry), `shouldAutoRelease` (idle gating), `parseIdleReleaseMs` (fallback su input invalido, clamp a `minMs`).
- `sync.test.mjs`: `shouldApplyRemoteContent` (apply su delta vivente, ignora snapshot iniziale con changes pending, ignora content uguale) e `shouldFlushPendingLocalChanges` (flush solo dopo l'initial snapshot).
- `offline-guard.test.mjs`: state iniziale, transizione `setOnline` (boolean return solo su cambio effettivo), coercion truthy/falsy.
- `sw-routes.test.mjs`: classificazione delle richieste (asset statici cached, shell HTML network-first, snapshot/listing SWR, file blob cache-first, bundle/POST/PUT/DELETE/admin/healthz/manifest/sw passthrough, query string ininfluente, URL malformati passthrough).

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
  main.go                  bootstrap, env parsing, route mount, cleanup/vacuum goroutines
  config.go                loader env → appConfig
  config_test.go           tabella env + fallback
  sw.js.tmpl               template service worker (cache name = version)
  templates/
    index.html             landing (form a due modalità)
    session.html           pagina sessione (editor + righe + toolbar + data-idle-release)
    admin.html             pannello admin
  static/
    style.css              CSS unico (desktop + mobile + admin + offline banner)
    manifest.webmanifest   PWA manifest (icone, theme color, display standalone)
    icon-*.png, *.svg      icone PWA (192, 512, maskable, apple-touch)
    favicon.svg            favicon
    app.js                 client sessione: WS, debounce, render, drag-drop, toggle mobile, E2E gating
    create.js              landing form: genera AES key client-side, appende #k=… al redirect
    crypto.js              wrapper Web Crypto AES-256-GCM (encrypt/decrypt text/bytes/name)
    bundle-client.js       ZIP builder client-side (STORE) per sessioni E2E
    e2e-state.js           state machine pura E2E (modes, classify incoming, guard anti-leak)
    blocks.js              parser blocchi (`-----`)
    countdown.js           helpers countdown (formattazione, msUntil, isExpired)
    download.js            helpers download client-side (sanitize filename, blob trigger)
    files.js               parser marker file, builder marker, formatBytes
    linkify.js             render anchor (rel="noopener noreferrer", target="_blank")
    commands.js            registry slash-command + parser + formatTimestamp
    lock.js                helpers lock client-side (state classification, heartbeat, idle release)
    sync.js                helpers sync (apply remote / flush pending)
    offline-guard.js       online/offline state, banner + read-only switch
    sw-routes.js           classificazione richieste service worker (testabile)
    admin.js               client admin: list, delete, mobile cards
    *.test.mjs             node:test (blocks, bundle-client, commands, countdown, crypto, download,
                           e2e-state, files, linkify, lock, offline-guard, sw-routes, sync)
internal/
  session/                 slug crypto-random + validazione nome
  store/
    store.go               sessions CRUD + scadenza + DeleteExpired + ListActive + secure_delete
    files.go               files CRUD + ReferencedFileIDs + DeleteOrphanFiles
    options.go             Options (FileBackend, MaxFilesPerSession, SecureDelete, ...)
  handlers/
    api.go                 CRUD sessioni HTTP (X-Client-ID, lock auto-acquire)
    ws.go                  WebSocket handler (OriginPatterns da ALLOWED_ORIGINS)
    hub.go                 pub/sub in-memory per stanza
    lock.go                LockManager TTL + heartbeat/acquire/release
    admin.go               basic auth (bcrypt + plaintext) + list + delete + audit + metrics
    audit.go               audit log admin
    middleware.go          security headers (CSP, HSTS, ...) + rate limit per-IP
    files.go               upload + download + list + bundle ZIP
  telemetry/
    metrics.go             contatori in-memory + endpoint admin
  version/
    version.go             Version var (ldflags-overridable)
scripts/
  bcrypt-hash/main.go      helper CLI per generare ADMIN_PASS_HASH
Dockerfile                 multi-stage, distroless, ARG VERSION
compose.yaml               servizio con volume + build-arg + tutte le env
Justfile                   ricette (run, build, test*, up, down, smoke)
README.md                  questo file
```
