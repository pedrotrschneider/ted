FROM node:24-bookworm-slim AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
COPY icon.png /icon.png
RUN npm run build

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY version.go version.txt ./
COPY cmd ./cmd
COPY internal ./internal
ARG RELEASE_VERSION
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X entertainmentdistrict.Version=${RELEASE_VERSION}" -o /ted ./cmd/ted

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/*
COPY --from=build /ted /usr/local/bin/ted
COPY LICENSE /usr/share/doc/ted/LICENSE
COPY third_party /usr/share/doc/ted/third_party
COPY --from=frontend /web/dist /opt/ted/web
ENV TED_WEB_DIR=/opt/ted/web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["ted"]
CMD ["serve"]
