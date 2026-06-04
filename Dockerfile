ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-alpine AS builder

ARG CGO_ENABLED=0
ARG GOOS=linux
ARG GOARCH=amd64

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /bin/controller ./cmd/controller

# ─── Runtime image ───────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/controller /controller

EXPOSE 8443 8080 8081

ENTRYPOINT ["/controller"]
