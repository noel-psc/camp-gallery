FROM docker.io/library/golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY templates ./templates
COPY static ./static
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/camp . \
    && mkdir /out/data && chown 1000:1000 /out/data

FROM scratch
ENV TZ=Asia/Shanghai DATA_DIR=/data LISTEN_ADDR=:8000 GOMEMLIMIT=512MiB
COPY --from=build /out/camp /camp
COPY --from=build --chown=1000:1000 /out/data /data
USER 1000:1000
WORKDIR /data
EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["/camp", "healthcheck"]
ENTRYPOINT ["/camp"]
