FROM rust:1.88-alpine AS worker
RUN apk add --no-cache musl-dev
WORKDIR /src/worker-server-rust
COPY worker-server-rust/Cargo.toml worker-server-rust/Cargo.lock ./
COPY worker-server-rust/src ./src
RUN cargo build --locked --release --bin lingosbot

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY docker-webui/go.mod docker-webui/go.sum* ./
RUN go mod download
COPY docker-webui/main.go ./
COPY docker-webui/internal ./internal
RUN CGO_ENABLED=0 go build -o /lingos-webui .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /lingos-webui /usr/local/bin/webui
COPY --from=worker /src/worker-server-rust/target/release/lingosbot /usr/local/bin/lingosbot
VOLUME /data
ENV DB_PATH=/data/webui.db LISTEN=:8080 WORKER_BIN=/usr/local/bin/lingosbot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/webui"]
