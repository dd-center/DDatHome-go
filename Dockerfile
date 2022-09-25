FROM golang:1.16-alpine AS builder
WORKDIR /builder
COPY . /builder
RUN apk add upx && \
    GO111MODULE=on go build -ldflags="-s -w" -o /ddathome && \
    upx --lzma --best /ddathome

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /ddathome /
CMD ["/ddathome"]