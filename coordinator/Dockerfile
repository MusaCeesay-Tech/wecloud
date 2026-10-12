# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /wecloud-coordinator ./...

FROM alpine:3.19
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /wecloud-coordinator /app/wecloud-coordinator
VOLUME ["/data"]
EXPOSE 8080
ENV WECLOUD_DB_PATH=/data/wecloud.db
CMD ["/app/wecloud-coordinator"]