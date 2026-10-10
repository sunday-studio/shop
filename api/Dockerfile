FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X shop/internal/version.Version=${VERSION} -X shop/internal/version.Commit=${COMMIT}" \
    -o /out/shop ./cmd/shop \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shop /usr/local/bin/shop
# Named volumes inherit this ownership, so the nonroot user can write the DB.
COPY --from=build --chown=65532:65532 /out/data /data
ENV SHOP_DATA_DIR=/data SHOP_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/shop"]
CMD ["serve"]
