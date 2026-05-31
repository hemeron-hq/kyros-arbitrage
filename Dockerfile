FROM golang:1.26.1-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kyros-arbitrage ./cmd/server \
    && mkdir -p /out/var-lib-app

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build --chown=nonroot:nonroot /out/kyros-arbitrage /app/kyros-arbitrage
COPY --from=build --chown=nonroot:nonroot /src/assets /app/assets
COPY --from=build --chown=nonroot:nonroot /src/sql/migrations /app/sql/migrations
COPY --from=build --chown=nonroot:nonroot /out/var-lib-app /var/lib/app

ENV ENV=production
ENV PORT=8090
ENV DATABASE_URL=file:/var/lib/app/app.db

EXPOSE 8090
VOLUME ["/var/lib/app"]

USER nonroot:nonroot
ENTRYPOINT ["/app/kyros-arbitrage"]
