# NGINX hardening — sharetext.prx.adiprint.it

Azioni da eseguire sulla Oracle Cloud VM che ospita `sharetext.prx.adiprint.it` dietro `nginx`. Tutti i comandi presuppongono accesso `ssh` con `sudo`. Riferimenti severity P0/P1 vengono dal piano di sicurezza in `/home/fabio/.claude/plans/witty-wibbling-iverson.md`.

---

## P0.1 — Upgrade nginx (CVE-2026-42945 "NGINX Rift" + 1.29.x CVE chain)

**Severity: Critical (CVSS 9.2 — RCE pre-auth se la config usa `rewrite` + capture non-named + `?` nella replacement).**

Versione attuale rilevata: `Server: nginx/1.29.2`. Target: `nginx 1.30.1` o `1.31.0`.

### Se nginx viene dal repo Oracle Linux / RHEL

```bash
sudo dnf check-update nginx
sudo dnf upgrade nginx
nginx -v   # expected: nginx version: nginx/1.30.1+
sudo systemctl restart nginx
```

### Se nginx viene dal repo ufficiale nginx.org

```bash
# Aggiungi/aggiorna repo (skip se già configurato)
sudo tee /etc/yum.repos.d/nginx.repo >/dev/null <<'EOF'
[nginx-stable]
name=nginx stable repo
baseurl=https://nginx.org/packages/centos/$releasever/$basearch/
gpgcheck=1
enabled=1
gpgkey=https://nginx.org/keys/nginx_signing.key
module_hotfixes=true
EOF
sudo dnf upgrade nginx
nginx -v
sudo systemctl restart nginx
```

### Workaround temporaneo se l'upgrade non è immediato

Solo se il vhost usa `rewrite` con `$1`/`$2`/.../`$9` e `?` nella replacement: convertire i capture in **named**.

```nginx
# PRIMA (vulnerabile a CVE-2026-42945)
location ~ ^/old-(\w+)/(\w+) {
    rewrite ^.*$ /new/$1?lookup=$2 last;
}
# DOPO (mitigato)
location ~ ^/old-(?<area>\w+)/(?<id>\w+) {
    rewrite ^.*$ /new/$area?lookup=$id last;
}
```

Verifica: `nginx -T 2>/dev/null | grep -E 'rewrite|\\\$[0-9]'` per individuare i pattern coinvolti.

---

## P0.2 — Rimuovere override degli header di sicurezza

**Severity: Critical (vanifica completamente l'hardening CSP dell'app).**

L'app emette già header stretti (CSP `default-src 'self'`, XFO `DENY`, Referrer `no-referrer`, Permissions-Policy disabling all). Nginx li sovrascrive con valori molto più permissivi.

### Stato corrente (dump da production)

```
Content-Security-Policy: default-src * 'unsafe-inline' 'unsafe-eval' data: blob:;
X-Frame-Options: SAMEORIGIN
Referrer-Policy: no-referrer-when-downgrade
Permissions-Policy: geolocation=(self), microphone=(self), camera=(self), fullscreen=(self)
X-XSS-Protection: 1; mode=block
```

### Fix vhost — opzione A (preferita): trust app headers

Nel file `/etc/nginx/conf.d/sharetext.conf` (o equivalente), nel `server {}` del vhost:

```nginx
server {
    listen 443 ssl http2;
    server_name sharetext.prx.adiprint.it;

    # TLS
    ssl_certificate     /etc/letsencrypt/live/prx.adiprint.it/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/prx.adiprint.it/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 10m;

    # Hide version
    server_tokens off;

    # NIENTE add_header globale nel server{} — lasciamo che l'app emetta CSP/XFO/Referrer/Permissions/HSTS.
    # add_header X-XSS-Protection ...;   <-- RIMUOVERE: header deprecato, può creare XS-Leak
    # add_header Content-Security-Policy ...;   <-- RIMUOVERE: l'app emette CSP stretta
    # add_header X-Frame-Options ...;   <-- RIMUOVERE
    # add_header Referrer-Policy ...;   <-- RIMUOVERE
    # add_header Permissions-Policy ...;   <-- RIMUOVERE

    # WebSocket upgrade
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;     # WS può restare aperto a lungo
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
        client_max_body_size 12m;     # > MAX_FILE_SIZE + multipart slack
        proxy_buffering off;          # streaming download/upload
    }
}

# Redirect HTTP → HTTPS
server {
    listen 80;
    server_name sharetext.prx.adiprint.it;
    return 301 https://$host$request_uri;
}
```

### Fix vhost — opzione B: replicare la CSP dell'app in nginx

Se preferisci che gli header siano emessi dal proxy (più visibili in audit), copia esattamente i valori che l'app userebbe:

```nginx
add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:; worker-src 'self'; manifest-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'" always;
add_header X-Frame-Options "DENY" always;
add_header Referrer-Policy "no-referrer" always;
add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
# NIENTE X-XSS-Protection (deprecato).
```

Importante: usa `always` per emettere gli header anche su risposte di errore (4xx/5xx). E **rimuovi** ogni `add_header` precedente o ereditato da `http {}` che imposti CSP/XFO/Referrer/Permissions.

### Server-tokens off (rimuove version disclosure)

In `/etc/nginx/nginx.conf` blocco `http {}`:

```nginx
http {
    server_tokens off;
    ...
}
```

Se hai il modulo `headers_more` installato, puoi anche cancellare completamente l'header `Server`:

```nginx
more_clear_headers Server;
```

### Reload + verifica

```bash
sudo nginx -t && sudo systemctl reload nginx

curl -sI https://sharetext.prx.adiprint.it/ | \
  grep -iE 'csp|content-security|frame|referrer|permissions|xss|server|strict-transport'
```

Atteso:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; ...
X-Frame-Options: DENY
Referrer-Policy: no-referrer
Permissions-Policy: camera=(), microphone=(), geolocation=()
Strict-Transport-Security: max-age=31536000; includeSubDomains
Server: nginx
```

Niente `X-XSS-Protection`. CSP **stretta**.

---

## P1 — Hardening secondario

### Cifre TLS — verifica grado

```bash
# Da locale (richiede testssl.sh installato)
testssl.sh --severity HIGH sharetext.prx.adiprint.it

# Oppure usa SSL Labs (esterno)
# https://www.ssllabs.com/ssltest/analyze.html?d=sharetext.prx.adiprint.it
```

Target: grado A o A+ (HSTS, no TLS 1.0/1.1, no RC4/3DES, perfect forward secrecy).

### Rate limiting nginx-side (defense-in-depth)

L'app ha un rate limiter per-IP, ma layer extra a livello nginx blocca prima i flood:

```nginx
# Nel blocco http {}
limit_req_zone $binary_remote_addr zone=stwrites:10m rate=20r/s;
limit_req_zone $binary_remote_addr zone=stadmin:10m rate=5r/s;

# Nel server {}
location /api/ {
    limit_req zone=stwrites burst=60 nodelay;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
location /admin {
    limit_req zone=stadmin burst=15 nodelay;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

### `client_max_body_size`

Già nell'esempio sopra: `12m` (allinearsi a `MAX_FILE_SIZE=10485760` + slack multipart). Senza questo nginx rifiuta upload con `413` prima che l'app possa rispondere.

### Body size per WS

`coder/websocket` legge solo frame ≤ `MaxContentSize + 1024`. Nginx con `proxy_buffering off` non bufferizza, OK. Verifica che `client_body_buffer_size` sia coerente (default 8k OK).

### Block metodi HTTP non usati

L'app risponde a `GET/POST/PUT/DELETE`. Bloccare `TRACE/CONNECT/PATCH` (anche se Go le rifiuterebbe comunque, è defense-in-depth):

```nginx
if ($request_method !~ ^(GET|HEAD|POST|PUT|DELETE|OPTIONS)$) {
    return 405;
}
```

### Logging — non loggare query string sensibili

Se mai dovessero apparire chiavi nei log (NB: il fragment `#k=...` non finisce mai in access log perché non viaggia in HTTP), puoi escludere query string completamente:

```nginx
log_format minimal '$remote_addr - $remote_user [$time_local] '
                   '"$request_method $uri $server_protocol" '
                   '$status $body_bytes_sent '
                   '"$http_referer" "$http_user_agent"';
access_log /var/log/nginx/sharetext.access.log minimal;
```

(usa `$uri` invece di `$request`, che taglia la query).

---

## P2 — Operational

### fail2ban su /admin (opzionale)

Se l'admin è accessibile da Internet, filtrare 401 ripetuti:

```ini
# /etc/fail2ban/filter.d/sharetext-admin.conf
[Definition]
failregex = ^<HOST> - .* "(GET|POST) /admin.*" 401
ignoreregex =

# /etc/fail2ban/jail.local
[sharetext-admin]
enabled = true
filter  = sharetext-admin
logpath = /var/log/nginx/sharetext.access.log
maxretry = 5
findtime = 300
bantime = 3600
```

### Certbot renewal hook

Verifica che `certbot renew` faccia reload nginx (non restart, per non killare WS attivi):

```bash
sudo systemctl cat certbot-renew.timer  # verifica abilitato
ls /etc/letsencrypt/renewal-hooks/deploy/   # deve esistere uno script che fa: systemctl reload nginx
```

### Backup pre-changes

Prima di toccare la config:

```bash
sudo cp -a /etc/nginx /etc/nginx.bak-$(date +%Y%m%d)
sudo nginx -T > /tmp/nginx-current-config.txt   # dump completo
```

---

## Verifica finale (post-deploy)

```bash
# 1. Versione
ssh vps "nginx -v"  # >= 1.30.1

# 2. Header
curl -sI https://sharetext.prx.adiprint.it/ | sort

# 3. TLS
echo | openssl s_client -connect sharetext.prx.adiprint.it:443 -servername sharetext.prx.adiprint.it -tls1_3 2>/dev/null | grep -E 'Protocol|Cipher'

# 4. WS upgrade da Origin valido
wscat -c "wss://sharetext.prx.adiprint.it/ws/<slug>" -H "Origin: https://sharetext.prx.adiprint.it"
# atteso: connessione stabilita

# 5. WS upgrade da Origin diverso
wscat -c "wss://sharetext.prx.adiprint.it/ws/<slug>" -H "Origin: https://evil.example"
# atteso: handshake rejected (403)

# 6. SSL Labs
# https://www.ssllabs.com/ssltest/analyze.html?d=sharetext.prx.adiprint.it
```

---

## Riassunto azioni

| # | Azione | File | Tempo stimato |
|---|---|---|---|
| 1 | Upgrade `nginx` a 1.30.1+ | repo pkg | 5 min |
| 2 | Rimuovere `add_header` permissivi dal vhost | `/etc/nginx/conf.d/sharetext.conf` | 10 min |
| 3 | `server_tokens off` | `/etc/nginx/nginx.conf` | 1 min |
| 4 | Verifica `proxy_set_header X-Real-IP/X-Forwarded-For/X-Forwarded-Proto` | vhost | 2 min |
| 5 | (opzionale) `limit_req` zones per /api/ e /admin | `/etc/nginx/nginx.conf` + vhost | 5 min |
| 6 | (opzionale) Block metodi HTTP non usati | vhost | 1 min |
| 7 | Reload `sudo systemctl reload nginx` | — | 1 min |
| 8 | Verifica `curl -sI` + `openssl s_client` | — | 5 min |

Totale: ~30 min, P0 (passi 1-4 + 7-8) sono mandatory.
