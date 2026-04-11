FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN export GOOS="${TARGETOS:-$(go env GOOS)}" && \
    export GOARCH="${TARGETARCH:-$(go env GOARCH)}" && \
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /collection-sync \
    ./cmd/collection-sync

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /collection-sync /collection-sync

USER 1000:1000

ENTRYPOINT ["/collection-sync"]
CMD ["run"]
