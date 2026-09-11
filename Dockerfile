FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o /kind-game main.go
FROM alpine:3.20
WORKDIR /app
COPY --from=build /kind-game /app/kind-game
COPY static /app/static
EXPOSE 443
ENV GAME_DURATION=30
CMD ["/app/kind-game"]
