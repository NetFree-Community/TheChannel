FROM node:20 as builder1

WORKDIR /app
COPY ./frontend .
RUN  npm install \
    && npm run build

FROM golang:1.24 AS builder2

WORKDIR /app

COPY ./backend .
COPY --from=builder1 /app/dist/channel/browser/favicon.ico assets
RUN go mod tidy
RUN go build -o the-channel .

FROM debian:latest
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates && update-ca-certificates && apt-get install -y curl
COPY --from=builder2 /app/the-channel . 
COPY --from=builder1 /app/dist/channel/browser /usr/share/ng
RUN chmod +x the-channel
CMD ["./the-channel"]
