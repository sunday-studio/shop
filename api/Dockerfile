FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X pantry/internal/version.Version=${VERSION} -X pantry/internal/version.Commit=${COMMIT}" \
    -o /out/pantry ./cmd/pantry \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pantry /usr/local/bin/pantry
# Named volumes inherit this ownership, so the nonroot user can write the DB.
COPY --from=build --chown=65532:65532 /out/data /data
ENV PANTRY_DATA_DIR=/data PANTRY_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pantry"]
CMD ["serve"]
