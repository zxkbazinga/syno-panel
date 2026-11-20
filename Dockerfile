FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${APP_VERSION}" -o /syno-panel .

FROM alpine:3.20
LABEL org.opencontainers.image.vendor="zzbazinga"
WORKDIR /app
COPY --from=build /syno-panel /usr/local/bin/syno-panel
USER 0:0
EXPOSE 5666
ENTRYPOINT ["/usr/local/bin/syno-panel"]
