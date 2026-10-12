# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /wecloud-proxy ./...

FROM alpine:3.19
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /wecloud-proxy /app/wecloud-proxy
EXPOSE 8081
CMD ["/app/wecloud-proxy"]