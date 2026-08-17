FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/agent-trace-processor ./cmd/agent-trace-processor

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/agent-trace-processor /usr/local/bin/agent-trace-processor
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/agent-trace-processor"]
