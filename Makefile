sock2rtm: *.go go.* Makefile cmd/sock2rtm/*.go
	cd cmd/sock2rtm && go build -o ../../sock2rtm

clean:
	rm -f sock2rtm

run: sock2rtm
	DEBUG=t ./sock2rtm

dist/:
	goreleaser build --snapshot --rm-dist
