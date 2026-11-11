FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kloxus ./cmd/kloxus

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --create-home kloxus \
	&& mkdir -p /data \
	&& chown -R kloxus:kloxus /data
USER kloxus
WORKDIR /app
COPY --from=build /out/kloxus /app/kloxus
ENV KLOXUS_MODE=demo \
	KLOXUS_PORT=8080 \
	KLOXUS_DATA_DIR=/data \
	KLOXUS_OUT_DIR=/data/out \
	KLOXUS_NO_BROWSER=1
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/kloxus", "demo"]
