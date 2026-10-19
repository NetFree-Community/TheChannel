FROM node:20 AS builder1

WORKDIR /app

# Copy manifests first so the dependency layer is cached when only source changes.
COPY ./frontend/package.json ./frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY ./frontend .
RUN npm run build

FROM golang:1.25 AS builder2

WORKDIR /app

# Copy dependency manifests first so this layer is cached when only source changes
COPY ./backend/go.mod ./backend/go.sum ./
RUN go mod download

COPY ./backend .
COPY --from=builder1 /app/dist/channel/browser/favicon.ico assets
RUN go build -o the-channel .

FROM debian:latest
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates && update-ca-certificates
COPY --from=builder2 /app/the-channel .
# The platform favicon is served from ./assets by the backend; without this
# line only the build stage had it and every tab showed no icon.
COPY --from=builder2 /app/assets ./assets
COPY --from=builder1 /app/dist/channel/browser /usr/share/ng
RUN chmod +x the-channel
CMD ["./the-channel"]
