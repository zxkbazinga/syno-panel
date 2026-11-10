FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${APP_VERSION}" -o /nas-panel .

FROM alpine:3.20
WORKDIR /app
COPY --from=build /nas-panel /usr/local/bin/nas-panel
USER 0:0
EXPOSE 5666
ENTRYPOINT ["/usr/local/bin/nas-panel"]
