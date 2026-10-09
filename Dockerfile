# syntax=docker/dockerfile:1

# The web and build stages run on the build machine's platform and cross-compile for
# the target platform, so multi-arch builds need no emulation.
FROM --platform=$BUILDPLATFORM node:26.10.0-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/dviz ./cmd/dviz

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/dviz /dviz
# A config file is required: mount it at /dviz.yml (the working directory is /) or
# /etc/dviz/dviz.yml, with listen: 0.0.0.0:8080 to be reachable through -p and
# headless: true (the image has no browser or xdg-open).
# Access to the Docker socket needs its group:
#   --group-add "$(stat -c %g /var/run/docker.sock)"
USER 65534:65534
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/dviz"]
