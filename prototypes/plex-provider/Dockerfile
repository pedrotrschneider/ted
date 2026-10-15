FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache ca-certificates
COPY bin/provider-linux-arm64 /usr/local/bin/provider
USER 1000:1000
EXPOSE 42390
ENTRYPOINT ["/usr/local/bin/provider"]
