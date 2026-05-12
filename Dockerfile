FROM golang:1.26-alpine AS build
ARG VERSION=""
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -eux; \
    LDFLAGS='-s -w'; \
    if [ -n "$VERSION" ]; then LDFLAGS="$LDFLAGS -X sharetext/internal/version.Version=$VERSION"; fi; \
    CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o /out/sharetext ./cmd/server
RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=build /out/sharetext /app/sharetext
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV PORT=8080 DB_PATH=/data/sharetext.db SLUG_LEN=16
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/sharetext"]
