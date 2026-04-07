FROM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /maintainerr-bridge \
    ./cmd/maintainerr-bridge

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /maintainerr-bridge /maintainerr-bridge

ENTRYPOINT ["/maintainerr-bridge"]
