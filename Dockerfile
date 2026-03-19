FROM docker.io/library/golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/alice-server ./cmd/server

FROM docker.io/library/alpine:3.20

RUN adduser -D -u 10001 alice

WORKDIR /app

COPY --from=build /out/alice-server /app/alice-server

USER alice

EXPOSE 8080

ENTRYPOINT ["/app/alice-server"]
