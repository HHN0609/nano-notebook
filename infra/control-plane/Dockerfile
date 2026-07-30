FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/nano-control-plane ./cmd/control-plane

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/nano-control-plane /usr/local/bin/nano-control-plane
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/nano-control-plane"]
