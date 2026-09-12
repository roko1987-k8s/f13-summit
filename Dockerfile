# La etapa de build corre en la arquitectura del host y compila cruzado
# para la de destino (TARGETARCH), así se puede generar linux/amd64 desde
# un Mac con Apple Silicon sin emulación:
#   podman build --platform linux/amd64 -t rcronald/f13-game:latest .
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /kind-game .

# La etapa final no ejecuta ningún RUN: solo copia archivos, por lo que
# tampoco necesita emulación. El usuario se indica por uid (no root).
FROM alpine:3.20
WORKDIR /app
COPY --from=build /kind-game /app/kind-game
COPY static /app/static
USER 10001:10001
EXPOSE 8080
ENV PORT=8080 \
    GAME_DURATION=30
CMD ["/app/kind-game"]
