FROM golang:1.23 AS build

WORKDIR /src

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations

RUN go build -trimpath -ldflags "-s -w" -o /out/api ./cmd/api \
    && go build -trimpath -ldflags "-s -w" -o /out/fakeapi ./cmd/fakeapi \
    && go build -trimpath -ldflags "-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static:nonroot AS runtime

COPY --from=build /out/api /api

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/api"]

FROM gcr.io/distroless/static:nonroot AS migrate

COPY --from=build /out/migrate /migrate

USER nonroot:nonroot

ENTRYPOINT ["/migrate"]

FROM gcr.io/distroless/static:nonroot AS fakeapi

COPY --from=build /out/fakeapi /fakeapi

USER nonroot:nonroot

EXPOSE 9401 9402 9403

ENTRYPOINT ["/fakeapi"]
