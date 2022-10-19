GIT_VER := $(shell git describe --tags)

sock2rtm: *.go go.* Makefile cmd/sock2rtm/*.go
	cd cmd/sock2rtm && go build -o ../../sock2rtm

clean:
	rm -f sock2rtm

run: sock2rtm
	DEBUG=t ./sock2rtm

dist/:
	goreleaser build --snapshot --rm-dist

docker-push: dist/
        rsync -av dist/sock2rtm_linux_amd64_v1 dist/sock2rtm_linux_amd64
        docker buildx build \
                --build-arg VERSION=${GIT_VER} \
                --platform linux/amd64,linux/arm64 \
                -f Dockerfile \
                -t ghcr.io/fujiwara/sock2rtm:${GIT_VER} \
                --push \
                .
