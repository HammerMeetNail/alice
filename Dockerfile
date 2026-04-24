FROM docker.io/library/golang:1.23-alpine AS build

# CMD_PATH selects which binary to build (default: coordination server).
# Override with --build-arg CMD_PATH=cmd/mcp-server or cmd/edge-agent.
ARG CMD_PATH=cmd/server

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/alice-bin ./${CMD_PATH}

FROM docker.io/library/alpine:3.20

RUN adduser -D -u 10001 alice

WORKDIR /app

COPY --from=build /out/alice-bin /app/alice-bin

USER alice

EXPOSE 8080

ENTRYPOINT ["/app/alice-bin"]
