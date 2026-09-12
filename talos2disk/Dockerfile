FROM golang:1.26-alpine AS talos2disk
RUN apk add --no-cache ca-certificates
COPY . /src
WORKDIR /src/talos2disk
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    --mount=type=cache,sharing=locked,id=goroot,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o talos2disk

FROM scratch
# Add Certificates into the image, for anything that does HTTPS calls
COPY --from=talos2disk /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=talos2disk /src/talos2disk/talos2disk /usr/bin/talos2disk
ENTRYPOINT ["/usr/bin/talos2disk"]
