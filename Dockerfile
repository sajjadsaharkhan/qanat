# Qanat image: both binaries, one image (PRD §15.3). Pick the binary with the
# container command: `gateway` or `control`.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/control ./cmd/control

FROM alpine:3.22 AS qanat
RUN apk add --no-cache ca-certificates tzdata wget \
 && adduser -D -H -u 10001 qanat
COPY --from=build /out/gateway /out/control /usr/local/bin/
USER qanat
EXPOSE 8080 8081
