FROM alpine:3.16.2
RUN apk add --no-cache ca-certificates
ARG VERSION
ARG TARGETARCH
ADD dist/sock2rtm_linux_${TARGETARCH}/sock2rtm /usr/local/bin/sock2rtm
ENTRYPOINT ["/usr/local/bin/sock2rtm"]
