# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

ENV GOTOOLCHAIN=go1.25.1+auto

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/openai-proxy ./cmd/server

FROM gcr.io/distroless/base-debian12 AS runtime
WORKDIR /app

COPY --from=build /app/bin/openai-proxy /app/openai-proxy
COPY --from=build /app/web /app/web

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/app/openai-proxy"]
