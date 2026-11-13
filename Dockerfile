# frontend: Vite builds the React app into internal/web/appdist, which
# the Go build below embeds into the binary.
FROM node:24-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/internal/web/appdist ./internal/web/appdist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kloxus ./cmd/kloxus

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --create-home kloxus \
	&& mkdir -p /data \
	&& chown -R kloxus:kloxus /data
USER kloxus
WORKDIR /app
COPY --from=build /out/kloxus /app/kloxus
# KLOXUS_MODE is a build-time default only — fly.toml overrides it per
# app (kloxus runs real, kloxus-demo stays on fixtures).
ENV KLOXUS_MODE=demo \
	KLOXUS_PORT=8080 \
	KLOXUS_DATA_DIR=/data \
	KLOXUS_OUT_DIR=/data/out \
	KLOXUS_NO_BROWSER=1
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/kloxus", "serve"]
