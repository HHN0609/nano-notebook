FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/nano-fetcher ./cmd/fetcher

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/nano-fetcher /usr/local/bin/nano-fetcher
USER 65532:65532
EXPOSE 8083
ENTRYPOINT ["/usr/local/bin/nano-fetcher"]
