# syntax=docker/dockerfile:1

FROM node:26.10.0-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/dviz ./cmd/dviz
RUN mkdir -p /out/etc/dviz && printf 'listen: 0.0.0.0:8080\n' > /out/etc/dviz/dviz.yml

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/dviz /dviz
COPY --from=build /out/etc/dviz/dviz.yml /etc/dviz/dviz.yml
# Mount /dviz.yml (highest priority, the working directory is /) or replace
# /etc/dviz/dviz.yml to configure. Access to the Docker socket needs its group:
#   --group-add "$(stat -c %g /var/run/docker.sock)"
USER 65534:65534
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/dviz"]
