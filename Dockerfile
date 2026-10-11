# syntax=docker/dockerfile:1

# --- build ---------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first so the layer is cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The store uses modernc/sqlite (pure Go), so the binary is fully static.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/vinted-notifier .

# --- runtime -------------------------------------------------------------
FROM alpine:3.22

# HTTPS to Vinted and the Matrix homeserver needs root certs; tzdata keeps
# timestamps sane if TZ is set.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 -h /data vinted

COPY --from=build /out/vinted-notifier /usr/local/bin/vinted-notifier

# /data is the host-mounted directory: config.yaml in, vinted.db out.
VOLUME /data
WORKDIR /data
USER vinted

ENTRYPOINT ["/usr/local/bin/vinted-notifier"]
CMD ["-config", "/data/config.yaml"]
